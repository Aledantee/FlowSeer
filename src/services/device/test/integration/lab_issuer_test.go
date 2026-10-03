//go:build authz_integration

package integration_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
)

func TestLabDexIssuer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Dex integration test under -short")
	}

	ctx := context.Background()
	tempDir := t.TempDir()

	certPath, keyPath, certPEM := generateTestTLSCert(t, tempDir)

	const (
		clientSecret = "test-lab-secret"
		alicePass    = "alice-secret-password"
		adminPass    = "admin-secret-password"
	)

	aliceHash, err := bcrypt.GenerateFromPassword([]byte(alicePass), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt alice: %v", err)
	}
	adminHash, err := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt admin: %v", err)
	}

	dexCfgBytes := labFixture(t, "dex/config.yaml")
	dexCfgPath := filepath.Join(tempDir, "dex-config.yaml")
	if err := os.WriteFile(dexCfgPath, dexCfgBytes, 0o644); err != nil {
		t.Fatalf("write dex config: %v", err)
	}

	dexCtr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: dexImage,
			Cmd:   []string{"dex", "serve", "/etc/dex/config.yaml"},
			Env: map[string]string{
				"DEX_LAB_CLIENT_SECRET": clientSecret,
				"DEX_USER_ALICE_HASH":   string(aliceHash),
				"DEX_USER_ADMIN_HASH":   string(adminHash),
			},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: dexCfgPath, ContainerFilePath: "/etc/dex/config.yaml", FileMode: 0o644},
				{HostFilePath: certPath, ContainerFilePath: "/etc/dex/tls.crt", FileMode: 0o644},
				{HostFilePath: keyPath, ContainerFilePath: "/etc/dex/tls.key", FileMode: 0o644},
			},
			ExposedPorts: []string{"8445/tcp"},
			WaitingFor:   wait.ForListeningPort("8445/tcp").WithStartupTimeout(30 * time.Second),
		},
		Started: true,
	})
	t.Cleanup(func() {
		if dexCtr == nil {
			return
		}
		_ = dexCtr.Terminate(context.Background())
	})
	if err != nil {
		t.Fatalf("start dex container: %v", err)
	}

	host, err := dexCtr.Host(ctx)
	if err != nil {
		t.Fatalf("dex host: %v", err)
	}
	port, err := dexCtr.MappedPort(ctx, "8445/tcp")
	if err != nil {
		t.Fatalf("dex port: %v", err)
	}

	// The issuer URL names 127.0.0.1:8445, so the client dials the container's
	// mapped port whenever it is asked for that address.
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(certPEM)
	targetAddr := net.JoinHostPort(host, port.Port())

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: certPool,
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr == "127.0.0.1:8445" {
				addr = targetAddr
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	discoURL := "https://127.0.0.1:8445/dex/.well-known/openid-configuration"
	ready := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, discoURL, nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		t.Fatal("timed out waiting for Dex discovery endpoint to become ready")
	}

	fetchToken := func(username, password, scope string) (string, error) {
		form := url.Values{
			"grant_type":    {"password"},
			"client_id":     {"flowseer-lab"},
			"client_secret": {clientSecret},
			"username":      {username},
			"password":      {password},
			"scope":         {scope},
		}
		resp, err := client.PostForm("https://127.0.0.1:8445/dex/token", form)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("token endpoint status %d: %s", resp.StatusCode, string(body))
		}
		var tokenResp struct {
			IDToken string `json:"id_token"`
		}
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			return "", err
		}
		if tokenResp.IDToken == "" {
			return "", fmt.Errorf("no id_token in response: %s", string(body))
		}
		return tokenResp.IDToken, nil
	}

	// The verifier is configured from the same issuer entry the service reads.
	centralCfg := &storev1.DeviceServiceConfig{}
	if err := prototext.Unmarshal(labFixture(t, "central.textproto"), centralCfg); err != nil {
		t.Fatalf("unmarshal central.textproto: %v", err)
	}

	issuers := centralCfg.GetAuthentication().GetIssuers()
	if len(issuers) == 0 {
		t.Fatal("central.textproto has no issuers")
	}

	fakeResolver := func(ctx context.Context, issuer, orgClaimValue string) (*identityv1.TenantRecord, error) {
		switch orgClaimValue {
		case "acme":
			return identityv1.TenantRecord_builder{
				Config: identityv1.TenantConfig_builder{
					OrganizationClaimName: proto.String("groups"),
					Ref: identityv1.TenantGlobalRef_builder{
						Tenant: identityv1.TenantLocalRef_builder{Id: proto.String("tenant-acme")}.Build(),
					}.Build(),
				}.Build(),
			}.Build(), nil
		case "globex":
			return identityv1.TenantRecord_builder{
				Config: identityv1.TenantConfig_builder{
					OrganizationClaimName: proto.String("groups"),
					Ref: identityv1.TenantGlobalRef_builder{
						Tenant: identityv1.TenantLocalRef_builder{Id: proto.String("tenant-globex")}.Build(),
					}.Build(),
				}.Build(),
			}.Build(), nil
		default:
			return nil, nil
		}
	}

	v, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:                issuers[0].GetIssuer(),
				Audience:              issuers[0].GetAudience(),
				OrganizationClaimName: issuers[0].GetOrganizationClaimName(),
			},
		},
		Platform: authn.PlatformConfig{
			Issuer:       issuers[0].GetIssuer(),
			ClaimName:    "groups",
			Organization: "flowseer-platform",
		},
		Resolver: fakeResolver,
		Client:   client,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	fullScope := "openid groups audience:server:client_id:flowseer-device"

	// alice is in acme and globex, so she yields two tenants.
	aliceToken, err := fetchToken("alice@flowseer.local", alicePass, fullScope)
	if err != nil {
		t.Fatalf("fetch alice token: %v", err)
	}
	p1, err := v.Verify(ctx, aliceToken)
	if err != nil {
		t.Fatalf("verify alice token: %v", err)
	}
	if len(p1.Tenants) != 2 || p1.Tenants[0] != "tenant-acme" || p1.Tenants[1] != "tenant-globex" {
		t.Errorf("alice tenants: got %v, want [tenant-acme, tenant-globex]", p1.Tenants)
	}
	if p1.Platform {
		t.Errorf("alice platform: got true, want false")
	}

	// admin is in flowseer-platform, so it yields Platform.
	adminToken, err := fetchToken("admin@flowseer.local", adminPass, fullScope)
	if err != nil {
		t.Fatalf("fetch admin token: %v", err)
	}
	p2, err := v.Verify(ctx, adminToken)
	if err != nil {
		t.Fatalf("verify admin token: %v", err)
	}
	if !p2.Platform {
		t.Errorf("admin platform: got false, want true")
	}

	// A token asked for without the audience scope carries no audience the
	// verifier accepts.
	noAudScope := "openid groups"
	noAudToken, err := fetchToken("alice@flowseer.local", alicePass, noAudScope)
	if err != nil {
		t.Fatalf("fetch token without audience scope: %v", err)
	}
	_, err = v.Verify(ctx, noAudToken)
	gotCode, ok := errs.CodeOf(err)
	if !ok || gotCode != authn.ErrCodeTokenInvalid {
		t.Fatalf("verify token without audience: got %v (code %v), want %v", err, gotCode, authn.ErrCodeTokenInvalid)
	}
}
