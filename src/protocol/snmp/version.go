package snmp

// scopeName is the OpenTelemetry instrumentation scope for this package —
// the package import path, per the OTel convention.
const scopeName = "go.aledante.io/FlowSeer/src/protocol/snmp"

// version is the instrumentation-scope version reported alongside
// [scopeName] when resolving the Tracer/Meter. It is a package-local
// constant rather than a build-stamped value; bump it when the emitted
// span/metric shape changes.
const version = "0.1.0"
