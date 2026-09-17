package host_test

import (
	"context"
	"strings"
	"testing"

	connect "connectrpc.com/connect"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/policy/v1"
)

func validApply() *devicev1.ApplyInterfaceDescriptionRequest {
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId("0192e6a0-0000-7000-8000-0000000000d1")
	device := &inventoryv1.DeviceGlobalRef{}
	device.SetDevice(local)

	operator := &accessv1.OperatorRef{}
	operator.SetSubject("zitadel|1")
	actor := &accessv1.Actor{}
	actor.SetOperator(operator)

	policy := &policyv1.AccessPolicyHandle{}
	policy.SetKey("icx7150-lab")
	policy.SetVersion(3)

	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName("ethernet 1/1/1")
	change.SetDescription("uplink to core")

	intent := &accessv1.MutationIntent{}
	intent.SetDevice(device)
	intent.SetIdempotencyKey("0192e6a0-0000-7000-8000-00000000a001")
	intent.SetActor(actor)
	intent.SetAccessPolicy(policy)
	intent.SetExpectedFirmwareFingerprint("ICX7150-24P SPS10010g")
	intent.SetInterfaceDescription(change)

	msg := &devicev1.ApplyInterfaceDescriptionRequest{}
	msg.SetIntent(intent)
	return msg
}

// An intent with no idempotency key used to reach the journal, which recorded
// an empty string as one of the sixty-four keys the device remembers — a value
// the schema says must be a uuid, consuming a slot and matching every other
// unkeyed intent. The handler never re-states the schema's rules, which is
// right, and this is what makes that safe.
func TestAnIntentMissingItsIdempotencyKeyNeverReachesTheHandler(t *testing.T) {
	handler := &failing{}
	client := serve(t, handler, nil)

	msg := validApply()
	msg.GetIntent().ClearIdempotencyKey()

	_, err := client.ApplyInterfaceDescription(context.Background(), connect.NewRequest(msg))
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want invalid_argument (%v)", got, err)
	}
	if handler.entered {
		t.Fatal("the handler ran on a request that fails its own schema rules")
	}
}

// An intent with no change arm was admitted with nothing to apply: it took the
// device's lane, ran, and left no expectation behind when it verified.
func TestAnIntentWithNoChangeArmNeverReachesTheHandler(t *testing.T) {
	handler := &failing{}
	client := serve(t, handler, nil)

	msg := validApply()
	msg.GetIntent().ClearInterfaceDescription()

	_, err := client.ApplyInterfaceDescription(context.Background(), connect.NewRequest(msg))
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want invalid_argument (%v)", got, err)
	}
	if handler.entered {
		t.Fatal("the handler ran on an intent with nothing to apply")
	}
}

// The refusal names what failed and which rule it broke, and never the value
// the caller sent: field paths and rule identifiers are this service's own
// vocabulary, and a value echoed into an error message reaches logs that were
// never meant to hold it.
func TestTheRefusalNamesTheFieldAndTheRuleAndNoValue(t *testing.T) {
	client := serve(t, &failing{}, nil)

	msg := validApply()
	msg.GetIntent().GetInterfaceDescription().SetDescription(strings.Repeat("x", 200))

	_, err := client.ApplyInterfaceDescription(context.Background(), connect.NewRequest(msg))
	if err == nil {
		t.Fatal("an over-long description was accepted")
	}
	// The whole rendered summary, not just that the word appears somewhere:
	// this used to be the prototext of the FieldPath message
	// (`elements:{field_name:"description"}`), which contains "description"
	// and satisfied the old assertion while promising a shape it did not
	// produce.
	const want = "intent.interface_description.description: string.max_len"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal reads %v, want it to name %q as field: rule", err, want)
	}
	if strings.Contains(err.Error(), "elements:") || strings.Contains(err.Error(), "field_name:") {
		t.Fatalf("the refusal carries the prototext of the field path rather than a field name: %v", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("x", 200)) {
		t.Fatalf("the refusal echoed the value back: %v", err)
	}
}

// A valid request passes through untouched, so the check costs a caller
// nothing when it has nothing to say.
func TestAValidRequestReachesTheHandler(t *testing.T) {
	handler := &failing{}
	client := serve(t, handler, nil)

	if _, err := client.ApplyInterfaceDescription(context.Background(), connect.NewRequest(validApply())); err == nil {
		t.Fatal("the handler's own failure was swallowed")
	}
	if !handler.entered {
		t.Fatal("a valid request did not reach the handler")
	}
}
