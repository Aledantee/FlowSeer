// Package packetio sends complete Ethernet frames through a named interface.
// It deliberately contains no receive path: capture and interface-drop
// accounting stay with src/modules/capture/rawsocket.
package packetio
