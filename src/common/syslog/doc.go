// Package syslog receives, parses, and sends network-device syslog messages.
// Records own their data and remain valid after another receive or Close.
// Receive time is an observation made at the socket, independent of device time.
// Protocol buffers, parser expansion, and connection occupancy have finite limits.
// Callers own delivery policy and memory retained after record handoff.
package syslog
