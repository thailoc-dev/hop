package tunnel

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   Class
	}{
		{"permission denied", "loc@host: Permission denied (publickey).", ClassAuth},
		{"too many auth failures", "Received disconnect from 10.0.0.1 port 22:2: Too many authentication failures", ClassAuth},
		{"host key changed", "Host key verification failed.", ClassAuth},
		{"unknown host", "ssh: Could not resolve hostname nope: nodename nor servname provided", ClassConfig},
		{"name resolution", "ssh: connect to host x: Name or service not known", ClassConfig},
		{"port busy", "bind [127.0.0.1]:27018: Address already in use\nchannel_setup_fwd_listener_tcpip: cannot listen to port: 27018", ClassLocalPort},
		{"refused", "ssh: connect to host h port 22: Connection refused", ClassNetwork},
		{"timeout", "ssh: connect to host h port 22: Operation timed out", ClassNetwork},
		{"no route", "ssh: connect to host h port 22: No route to host", ClassNetwork},
		{"unreachable", "ssh: connect to host h port 22: Network is unreachable", ClassNetwork},
		{"closed by remote", "Connection closed by remote host", ClassNetwork},
		{"broken pipe", "client_loop: send disconnect: Broken pipe", ClassNetwork},
		{"keepalive expiry", "Timeout, server 10.0.0.1 not responding.", ClassNetwork},
		{"no such container", `Error: No such object: app_mongo_staging`, ClassContainer},
		{"empty", "", ClassUnknown},
		{"gibberish", "something nobody has ever seen", ClassUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.stderr); got != tc.want {
				t.Fatalf("Classify(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}

func TestClassifyIsCaseInsensitive(t *testing.T) {
	if got := Classify("PERMISSION DENIED (publickey)."); got != ClassAuth {
		t.Fatalf("got %v, want ClassAuth", got)
	}
}

func TestClassifyPrefersFatalWhenBothPresent(t *testing.T) {
	// A retryable line must not mask a fatal one that appears alongside it.
	stderr := "ssh: connect to host h port 22: Connection refused\nPermission denied (publickey)."
	if got := Classify(stderr); got != ClassAuth {
		t.Fatalf("got %v, want ClassAuth", got)
	}
}

func TestFatalClasses(t *testing.T) {
	fatal := map[Class]bool{
		ClassAuth: true, ClassConfig: true, ClassLocalPort: true,
		ClassNetwork: false, ClassContainer: false, ClassUnknown: false,
	}
	for class, want := range fatal {
		if got := class.Fatal(); got != want {
			t.Fatalf("%v.Fatal() = %v, want %v", class, got, want)
		}
	}
}
