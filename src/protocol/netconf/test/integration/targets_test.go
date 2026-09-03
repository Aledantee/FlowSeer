package integration

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// t4Target is one live device.
type t4Target struct {
	Addr     string
	User     string
	Password string
}

// parseT4Targets parses the env contract.
func parseT4Targets(raw string) ([]t4Target, error) {
	var out []t4Target
	for i, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		addr, creds, ok := strings.Cut(entry, "@")
		if !ok || addr == "" {
			return nil, fmt.Errorf("entry %d: want host:port@user:password", i+1)
		}
		user, pass, ok := strings.Cut(creds, ":")
		if !ok || user == "" || pass == "" {
			return nil, fmt.Errorf("entry %d: want host:port@user:password", i+1)
		}
		if !strings.Contains(addr, ":") {
			addr = net.JoinHostPort(addr, "830")
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil || host == "" {
			return nil, fmt.Errorf("entry %d: want a host and port", i+1)
		}
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("entry %d: port must be between 1 and 65535", i+1)
		}
		out = append(out, t4Target{Addr: addr, User: user, Password: pass})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no targets parsed")
	}
	return out, nil
}
