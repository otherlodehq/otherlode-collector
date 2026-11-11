package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

const (
	healthzPath = "/healthz"

	// healthcheckTimeout must stay under the image HEALTHCHECK --timeout of 3s.
	healthcheckTimeout = 2 * time.Second
)

var healthcheckClient = &http.Client{Transport: &http.Transport{Proxy: nil}}

func runSubcommand(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "healthcheck" {
		fmt.Fprintln(stderr, "usage: otherlode-collector [healthcheck]")
		return 2
	}
	if err := healthcheckCommand(ctx, getenv); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func healthcheckCommand(ctx context.Context, getenv func(string) string) error {
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	target, err := healthzURL(ctx, resolveAddr(getenv("OTHERLODE_COLLECTOR_ADDR")))
	if err != nil {
		return err
	}
	return runHealthcheck(ctx, target)
}

func resolveAddr(raw string) string {
	if raw == "" {
		return defaultAddr
	}
	return raw
}

func healthzURL(ctx context.Context, addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("healthz url: %w", err)
	}
	portNum, err := net.DefaultResolver.LookupPort(ctx, "tcp", port)
	if err != nil {
		return "", fmt.Errorf("healthz url: %w", err)
	}
	if ip, err := netip.ParseAddr(host); host == "" || (err == nil && ip.WithZone("").Unmap().IsUnspecified()) {
		host = "127.0.0.1"
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(portNum)), Path: healthzPath}
	return u.String(), nil
}

func runHealthcheck(ctx context.Context, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("healthz: %w", err)
	}
	resp, err := healthcheckClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthz: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: %s", resp.Status)
	}
	return nil
}
