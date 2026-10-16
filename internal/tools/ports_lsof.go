package tools

import (
	"strconv"
	"strings"
)

// lsof fields describe a file record; the TCP state may follow its name.
func parseDarwinLsof(output, protocol string, requestedPort int, endpointsByPID map[int][]PortEndpoint, nameByPID map[int]string) {
	pid := 0
	var endpoint *PortEndpoint
	state := ""
	hasSocketFile := false
	flush := func() {
		if pid > 0 && endpoint != nil {
			endpoint.State = state
			endpointsByPID[pid] = appendUniquePortEndpoint(endpointsByPID[pid], *endpoint)
		}
		endpoint, state = nil, ""
	}
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 2 {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			flush()
			pid, _ = strconv.Atoi(value)
			hasSocketFile = false
		case 'c':
			if pid > 0 && value != "" {
				nameByPID[pid] = value
			}
		case 'f':
			flush()
			hasSocketFile = true
		case 'T':
			if strings.HasPrefix(value, "TST=") {
				state = strings.TrimPrefix(value, "TST=")
			} else if strings.HasPrefix(value, "ST=") {
				state = strings.TrimPrefix(value, "ST=")
			}
		case 'n':
			if pid <= 0 || !hasSocketFile {
				continue
			}
			address, port, ok := parseDarwinLsofAddress(value)
			if ok && (requestedPort == 0 || port == requestedPort) {
				endpoint = &PortEndpoint{Protocol: protocol, Address: address, Port: port}
			}
		}
	}
	flush()
}

func parseDarwinLsofAddress(value string) (string, int, bool) {
	local := strings.TrimSpace(value)
	if before, _, found := strings.Cut(local, " ("); found {
		local = before
	}
	if before, _, found := strings.Cut(local, "->"); found {
		local = before
	}
	if at := strings.LastIndexByte(local, ' '); at >= 0 {
		local = local[at+1:]
	}
	colon := strings.LastIndexByte(local, ':')
	if colon < 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(local[colon+1:])
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	return strings.Trim(local[:colon], "[]"), port, true
}

func appendUniquePortEndpoint(items []PortEndpoint, endpoint PortEndpoint) []PortEndpoint {
	for _, item := range items {
		if item == endpoint {
			return items
		}
	}
	return append(items, endpoint)
}
