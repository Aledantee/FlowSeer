package lanehost

// Exported for tests. The endpoint a listed device resolves to is not
// observable through a DeviceSession — it is captured inside the session
// factories — so the mapping from the listing to it is checked here rather
// than by opening a session against a device.
var (
	EndpointFor    = endpointFor
	DivergedFields = divergedFields
)
