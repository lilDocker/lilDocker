package runtime

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

type forward struct {
	ip, host, target string
}

// parsePort reads "[ip:]hostPort:containerPort[/tcp]".
func parsePort(spec string) (forward, error) {
	var f forward
	s := spec
	if i := strings.Index(s, "/"); i >= 0 {
		if s[i+1:] != "tcp" {
			return f, fmt.Errorf("%q: only tcp ports are supported", spec)
		}
		s = s[:i]
	}
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 2:
		f.host, f.target = parts[0], parts[1]
	case 3:
		f.ip, f.host, f.target = parts[0], parts[1], parts[2]
	default:
		return f, fmt.Errorf("invalid port mapping %q, expected hostPort:containerPort", spec)
	}
	for _, p := range []string{f.host, f.target} {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return f, fmt.Errorf("invalid port %q in %q", p, spec)
		}
	}
	return f, nil
}

// startForwards listens on the host ports and relays to the container's.
// Containers share the host network, so a mapping like 8080:3000 simply
// relays localhost:8080 to the program listening on 3000.
func startForwards(specs []string) (func(), error) {
	var listeners []net.Listener
	stop := func() {
		for _, l := range listeners {
			l.Close()
		}
	}
	for _, spec := range specs {
		f, err := parsePort(spec)
		if err != nil {
			stop()
			return func() {}, err
		}
		if f.host == f.target && (f.ip == "" || f.ip == "0.0.0.0") {
			continue
		}
		l, err := net.Listen("tcp", net.JoinHostPort(f.ip, f.host))
		if err != nil {
			stop()
			return func() {}, fmt.Errorf("publishing %s: %w", spec, err)
		}
		listeners = append(listeners, l)
		go serve(l, net.JoinHostPort("127.0.0.1", f.target))
	}
	return stop, nil
}

func serve(l net.Listener, target string) {
	for {
		in, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			out, err := net.Dial("tcp", target)
			if err != nil {
				in.Close()
				return
			}
			done := make(chan struct{}, 2)
			relay := func(dst, src net.Conn) {
				io.Copy(dst, src)
				done <- struct{}{}
			}
			go relay(out, in)
			go relay(in, out)
			<-done
			in.Close()
			out.Close()
		}()
	}
}
