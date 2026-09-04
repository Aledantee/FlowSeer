package service

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	maxModulePathLength  = 255
	maxDurableNameLength = 64
)

var (
	errCodeModuleName         = errs.NewCode("service/module-name")
	errCodeModuleShape        = errs.NewCode("service/module-shape")
	errCodeModuleCollision    = errs.NewCode("service/module-collision")
	errCodeEmptyEffectiveTree = errs.NewCode("service/empty-effective-tree")
	errCodeHandlerMismatch    = errs.NewCode("service/handler-mismatch")
)

// Module declares one stable node in a service's supervision tree. Exactly
// one of Leaf or Branch must be set. The zero value is invalid.
type Module struct {
	// Name is the module's stable lower-snake-case path segment.
	Name string
	// Gate controls whether this module participates in a supervisor generation.
	// Its zero value enables the module.
	Gate Gate
	// Policy controls how the owning supervisor handles this module's outcomes.
	// Its zero value stops normal returns and restarts errors and panics.
	Policy Policy
	// Leaf declares attempt-local execution.
	Leaf *Leaf
	// Branch declares a nested supervisor and its children.
	Branch *Branch
}

// Leaf declares the static contract for a module that performs work. Setup is
// called once for each attempt. The zero value is invalid.
type Leaf struct {
	// Setup constructs one fresh attempt.
	Setup SetupFunc
	// Subscriptions declares every handler that Setup must provide.
	Subscriptions []Subscription
	// DeliveryConcurrency bounds concurrent message handling. Zero means one.
	DeliveryConcurrency int
}

// Branch declares a nested supervisor. It must contain at least one child.
// The zero value is invalid.
type Branch struct {
	// Strategy selects the affected sibling set. The zero value is one-for-one.
	Strategy Strategy
	// Intensity bounds aggregate strategy applications in a rolling window.
	// Its zero value selects five applications per five minutes.
	Intensity RestartBudget
	// Children is ordered for stable identity and supervision policy.
	Children []Module
}

type plannedModule struct {
	path        string
	envKey      string
	pathToken   string
	durableName string
	gate        Gate
	policy      normalizedPolicy
	supervisor  normalizedSupervisor
	leaf        *plannedLeaf
	children    []plannedModule
	enabled     bool
}

type plannedLeaf struct {
	setup               SetupFunc
	subscriptions       []plannedSubscription
	deliveryConcurrency int
}

type derivedIdentitySet struct {
	values map[string]map[string]string
}

func newDerivedIdentitySet() *derivedIdentitySet {
	return &derivedIdentitySet{values: make(map[string]map[string]string)}
}

func (s *derivedIdentitySet) add(kind, value, path string) error {
	byValue := s.values[kind]
	if byValue == nil {
		byValue = make(map[string]string)
		s.values[kind] = byValue
	}
	if previous, ok := byValue[value]; ok && previous != path {
		return errs.New().
			Code(errCodeModuleCollision).
			Attr("module_path", path).
			Attr("conflicting_module_path", previous).
			Attr("identity_kind", kind).
			Attr("derived_identity", value).
			Msgf("module %s collides with %s on %s", path, previous, kind)
	}

	byValue[value] = path
	return nil
}

func validateDeclaration(config Config) (runtimeConfig, error) {
	if err := validateIdentity(config.Identity); err != nil {
		return runtimeConfig{}, err
	}

	envPrefix, err := normalizeEnvPrefix(config)
	if err != nil {
		return runtimeConfig{}, err
	}
	rootSupervisor, err := normalizeRootSupervisor(config.Strategy, config.Intensity)
	if err != nil {
		return runtimeConfig{}, err
	}

	hasImplicit := config.Setup != nil
	hasExplicit := len(config.Modules) != 0
	if hasImplicit == hasExplicit {
		return runtimeConfig{}, errs.New().
			Code(errCodeModuleShape).
			Attr("module_path", config.Identity.Name).
			Msg("service must declare exactly one implicit module or an explicit module list")
	}

	registry := newStaticRegistryBuilder()
	identities := newDerivedIdentitySet()
	var modules []plannedModule
	if hasImplicit {
		module := Module{Name: config.Identity.Name, Leaf: &Leaf{Setup: config.Setup}}
		planned, buildErr := validateModule(module, config.Identity.Name, config.Identity.Name, envPrefix, registry, identities)
		if buildErr != nil {
			return runtimeConfig{}, buildErr
		}
		modules = []plannedModule{planned}
	} else {
		modules, err = validateModuleList(config.Modules, config.Identity.Name, config.Identity.Name, envPrefix, registry, identities)
		if err != nil {
			return runtimeConfig{}, err
		}
	}

	return runtimeConfig{
		identity:          config.Identity,
		envPrefix:         envPrefix,
		modules:           modules,
		registry:          registry.build(),
		rootSupervisor:    rootSupervisor,
		telemetryShutdown: config.TelemetryShutdown,
	}, nil
}

func validateModuleList(
	modules []Module,
	parentPath string,
	rootPath string,
	envPrefix string,
	registry *staticRegistryBuilder,
	identities *derivedIdentitySet,
) ([]plannedModule, error) {
	seenNames := make(map[string]struct{}, len(modules))
	planned := make([]plannedModule, 0, len(modules))
	for _, module := range modules {
		path := parentPath + "/" + module.Name
		if _, ok := seenNames[module.Name]; ok {
			return nil, errs.New().
				Code(errCodeModuleCollision).
				Attr("module_path", path).
				Attr("module_name", module.Name).
				Msgf("module %s duplicates a sibling name", path)
		}
		seenNames[module.Name] = struct{}{}

		item, err := validateModule(module, path, rootPath, envPrefix, registry, identities)
		if err != nil {
			return nil, err
		}
		planned = append(planned, item)
	}

	return planned, nil
}

func validateModule(
	module Module,
	path string,
	rootPath string,
	envPrefix string,
	registry *staticRegistryBuilder,
	identities *derivedIdentitySet,
) (plannedModule, error) {
	if err := validateIdentitySegment("module name", module.Name); err != nil {
		return plannedModule{}, errs.From(err).
			Code(errCodeModuleName).
			Attr("module_path", path).
			Attr("module_name", module.Name).
			Msgf("module %s has an invalid name", path)
	}
	if len(path) > maxModulePathLength {
		return plannedModule{}, errs.New().
			Code(errCodeModuleName).
			Attr("module_path", path).
			Attr("path_length", len(path)).
			Attr("maximum_path_length", maxModulePathLength).
			Msgf("module path exceeds %d bytes", maxModulePathLength)
	}
	if err := validateGate(path, module.Gate); err != nil {
		return plannedModule{}, err
	}
	policy, err := normalizePolicy(module.Policy)
	if err != nil {
		return plannedModule{}, errs.From(err).
			Code(errCodeModuleShape).
			Attr("module_path", path).
			Msgf("module %s has an invalid outcome policy", path)
	}

	isLeaf := module.Leaf != nil
	isBranch := module.Branch != nil
	if isLeaf == isBranch {
		return plannedModule{}, errs.New().
			Code(errCodeModuleShape).
			Attr("module_path", path).
			Msgf("module %s must be exactly one leaf or branch", path)
	}
	if isLeaf && module.Leaf.Setup == nil {
		return plannedModule{}, errs.New().
			Code(errCodeModuleShape).
			Attr("module_path", path).
			Msgf("leaf module %s has no setup", path)
	}
	if isBranch && len(module.Branch.Children) == 0 {
		return plannedModule{}, errs.New().
			Code(errCodeModuleShape).
			Attr("module_path", path).
			Msgf("branch module %s has no children", path)
	}

	envKey := moduleEnvKey(envPrefix, rootPath, path)
	pathToken := encodeSubjectToken(path)
	durableName := durableConsumerName(path)
	for _, identity := range []struct {
		kind  string
		value string
	}{
		{kind: "environment_key", value: envKey},
		{kind: "subject_token", value: pathToken},
		{kind: "durable_name", value: durableName},
	} {
		if err := identities.add(identity.kind, identity.value, path); err != nil {
			return plannedModule{}, err
		}
	}

	planned := plannedModule{
		path:        path,
		envKey:      envKey,
		pathToken:   pathToken,
		durableName: durableName,
		gate:        module.Gate,
		policy:      policy,
	}
	if isLeaf {
		leaf, err := validateLeaf(path, *module.Leaf, registry)
		if err != nil {
			return plannedModule{}, err
		}
		planned.leaf = leaf
		registry.addModule(path)
		return planned, nil
	}

	registry.addModule(path)
	supervisor, err := normalizeSupervisor(module.Branch.Strategy, module.Branch.Intensity)
	if err != nil {
		return plannedModule{}, errs.From(err).
			Code(errCodeModuleShape).
			Attr("module_path", path).
			Msgf("module %s has an invalid supervisor policy", path)
	}
	planned.supervisor = supervisor
	children, err := validateModuleList(module.Branch.Children, path, rootPath, envPrefix, registry, identities)
	if err != nil {
		return plannedModule{}, err
	}
	planned.children = children
	return planned, nil
}

func validateLeaf(path string, leaf Leaf, registry *staticRegistryBuilder) (*plannedLeaf, error) {
	concurrency := leaf.DeliveryConcurrency
	if concurrency == 0 {
		concurrency = 1
	}
	if concurrency < 1 || concurrency > maxDeliveryConcurrency {
		return nil, subscriptionError(path, "delivery concurrency is outside its supported bounds").
			Attr("delivery_concurrency", leaf.DeliveryConcurrency).
			Attr("maximum_delivery_concurrency", maxDeliveryConcurrency).
			Msgf("module %s has invalid delivery concurrency", path)
	}

	subscriptions, err := registry.addSubscriptions(path, leaf.Subscriptions)
	if err != nil {
		return nil, err
	}

	return &plannedLeaf{
		setup:               leaf.Setup,
		subscriptions:       subscriptions,
		deliveryConcurrency: concurrency,
	}, nil
}

func (c runtimeConfig) modulePaths() []string {
	var paths []string
	var appendPaths func([]plannedModule)
	appendPaths = func(modules []plannedModule) {
		for _, module := range modules {
			paths = append(paths, module.path)
			appendPaths(module.children)
		}
	}
	appendPaths(c.modules)
	return paths
}

func moduleEnvKey(prefix, rootPath, path string) string {
	relative := strings.TrimPrefix(path, rootPath)
	relative = strings.TrimPrefix(relative, "/")
	if relative == "" {
		return strings.TrimSuffix(prefix, "_") + "_ENABLED"
	}
	return prefix + strings.ToUpper(strings.ReplaceAll(relative, "/", "_")) + "_ENABLED"
}

func encodeSubjectToken(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func durableConsumerName(path string) string {
	sum := sha256.Sum256([]byte(path))
	digest := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return "v1_" + strings.ToLower(digest)
}

func preflight(ctx context.Context, config Config, lookup envLookup) (runtimeConfig, error) {
	declaration, err := validateDeclaration(config)
	if err != nil {
		return runtimeConfig{}, err
	}

	modules, enabledLeaves, err := snapshotGates(ctx, declaration.modules, lookup, true)
	if err != nil {
		return runtimeConfig{}, err
	}
	if enabledLeaves == 0 {
		return runtimeConfig{}, errs.New().
			Code(errCodeEmptyEffectiveTree).
			Attr("module_path", declaration.identity.Name).
			Msg("service has no enabled leaf modules")
	}

	declaration.modules = modules
	declaration.admission = newAdmissionRevision(declaration.registry)
	if config.Bus != nil {
		bus, err := normalizeBusConfig(config.Identity, *config.Bus)
		if err != nil {
			return runtimeConfig{}, err
		}
		declaration.bus = &bus
	}
	return declaration, nil
}
