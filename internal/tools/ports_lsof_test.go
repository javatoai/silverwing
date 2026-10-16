package tools

import "testing"

func TestDarwinLsofCompleteFileRecords(t *testing.T) {
	output := "p42\ncserver\nf3\ntIPv4\nn*:8080\nTST=LISTEN\nf4\nTST=ESTABLISHED\nn[::1]:8080->[::1]:50000\np43\ncother\nf5\nn127.0.0.1:8080\nTST=CLOSE_WAIT\nf6\nn*:9090\nTST=LISTEN\n"
	endpoints := map[int][]PortEndpoint{}
	names := map[int]string{}
	parseDarwinLsof(output, "TCP", 8080, endpoints, names)
	if len(endpoints[42]) != 2 || endpoints[42][0].State != "LISTEN" || endpoints[42][1].State != "ESTABLISHED" || endpoints[42][1].Address != "::1" {
		t.Fatalf("TCP state or local IPv6 endpoint was lost: %#v", endpoints[42])
	}
	if len(endpoints[43]) != 1 || endpoints[43][0].State != "CLOSE_WAIT" || names[43] != "other" {
		t.Fatalf("process boundary or requested-port filter failed: %#v %#v", endpoints, names)
	}
	parseDarwinLsof("p42\ncserver\nf3\nn*:8080\nTST=LISTEN\n", "TCP", 8080, endpoints, names)
	if len(endpoints[42]) != 2 {
		t.Fatalf("duplicate file endpoint was retained: %#v", endpoints[42])
	}
}
