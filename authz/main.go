package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	listenAddr      = ":9000"
	checkPath       = "/check"
	headerSourceURL = "X-Source-Url"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc(checkPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			deny(w, fmt.Errorf("method not allowed: %s", r.Method))
			return
		}

		src := strings.TrimSpace(r.Header.Get(headerSourceURL))
		if src == "" {
			deny(w, errors.New("missing X-Source-Url header"))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 1500*time.Millisecond)
		defer cancel()

		if err := validateSourceURL(ctx, src); err != nil {
			deny(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}

	log.Printf("authz listening on %s", listenAddr)
	log.Fatal(srv.ListenAndServe())
}

func deny(w http.ResponseWriter, err error) {
	log.Printf("deny: %v", err)
	w.WriteHeader(http.StatusForbidden) // nginx maps this to 400
}

func validateSourceURL(ctx context.Context, raw string) error {
	if strings.Contains(raw, "\\") {
		return errors.New("backslash is not allowed")
	}
	// Reject obvious traversal indicators early (including common encodings)
	if containsTraversalIndicators(raw) {
		return errors.New("traversal-like patterns are not allowed")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url parse failed: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("scheme must be https, got %q", u.Scheme)
	}
	if u.User != nil {
		return errors.New("userinfo is not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("hostname is required")
	}
	// Ensure the path can't escape via cleaning surprises (defensive)
	clean := path.Clean(u.EscapedPath())
	if strings.Contains(clean, "..") {
		return errors.New("path cleaning resulted in traversal")
	}

	// If host is an IP literal, validate directly. Otherwise resolve and validate all IPs.
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("source ip is not allowed: %s", ip.String())
		}
		return nil
	}

	ips, err := lookupAllIPs(ctx, host)
	if err != nil {
		return fmt.Errorf("dns lookup failed: %w", err)
	}
	if len(ips) == 0 {
		return errors.New("dns lookup returned no addresses")
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("resolved ip is not allowed: %s", ip.String())
		}
	}
	return nil
}

func lookupAllIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	var r net.Resolver
	addrs, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	uniq := make(map[netip.Addr]struct{}, len(addrs))
	for _, a := range addrs {
		if ip, ok := netip.AddrFromSlice(a.IP); ok {
			uniq[ip.Unmap()] = struct{}{}
		}
	}
	out := make([]netip.Addr, 0, len(uniq))
	for ip := range uniq {
		out = append(out, ip)
	}
	return out, nil
}

func isPublicIP(ip netip.Addr) bool {
	// Require global unicast and not-private. Also exclude link-local, loopback, multicast, etc.
	if !ip.IsValid() {
		return false
	}
	if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if !ip.IsGlobalUnicast() {
		return false
	}
	// Go treats IPv4 private and IPv6 ULA (fc00::/7) as private.
	if ip.IsPrivate() {
		return false
	}
	return true
}

func containsTraversalIndicators(s string) bool {
	l := strings.ToLower(s)
	if strings.Contains(l, "/../") || strings.HasSuffix(l, "/..") || strings.HasPrefix(l, "../") || l == ".." {
		return true
	}
	// Percent-encoded variants. We look for any %2e and %2f combos suggesting traversal.
	if strings.Contains(l, "%2e") {
		// %2e%2e, %2e./, .%2e, etc.
		if strings.Contains(l, "%2e%2e") || strings.Contains(l, "%2e.") || strings.Contains(l, ".%2e") {
			return true
		}
		// %2e followed by / (encoded or literal) is suspicious enough for this service.
		if strings.Contains(l, "%2e/") || strings.Contains(l, "%2e%2f") || strings.Contains(l, "%2e\\") || strings.Contains(l, "%2e%5c") {
			return true
		}
	}
	// Encoded slash/backslash by itself isn't always bad, but combined with dots it often is.
	if strings.Contains(l, "%2f..") || strings.Contains(l, "%5c..") || strings.Contains(l, "%2f%2e") || strings.Contains(l, "%5c%2e") {
		return true
	}
	return false
}
