package platform

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultProxyPort = 1355
	proxyPortLast    = 1399
	proxyAttempts    = 5
)

type proxyPortPolicy struct {
	first    int
	last     int
	attempts int
}

var defaultProxyPortPolicy = proxyPortPolicy{
	first:    DefaultProxyPort,
	last:     proxyPortLast,
	attempts: proxyAttempts,
}

func (m Manager) ensurePortless(ctx context.Context, base Portless) (int, error) {
	if port, configured, err := explicitProxyPort(m.Env); err != nil {
		return 0, err
	} else if configured {
		router := base.withPort(port)
		ready, readyErr := router.Ready(ctx)
		if readyErr == nil && ready {
			return port, nil
		}
		if readyErr != nil && !portCollision(readyErr) {
			return 0, readyErr
		}
		if m.NonInteractive {
			return 0, fmt.Errorf("Portless proxy is not running; run cassie up to start it")
		}
		if err := router.StartOn(ctx, port); err != nil {
			return 0, fmt.Errorf("start Portless on configured port %d: %w", port, err)
		}
		return port, nil
	}

	return m.ensureAutomaticPortless(ctx, base, defaultProxyPortPolicy)
}

func (m Manager) ensureAutomaticPortless(ctx context.Context, base Portless, policy proxyPortPolicy) (int, error) {
	stored, _, err := readProxyPort(m.proxyPortPath())
	if err != nil {
		return 0, err
	}
	active := 0
	if stateDir := strings.TrimSpace(base.Env["PORTLESS_STATE_DIR"]); stateDir != "" {
		active, _, _ = readProxyPort(filepath.Join(stateDir, "proxy.port"))
	}
	if active >= policy.first && active <= policy.last {
		stored = active
	} else if active > 0 {
		router := base.withPort(active)
		ready, readyErr := router.Ready(ctx)
		if readyErr == nil && ready {
			if m.NonInteractive {
				return 0, fmt.Errorf("Portless is running on port %d; run cassie down, then cassie up to move it", active)
			}
			if base.Stdout != nil {
				_, _ = fmt.Fprintf(base.Stdout, "Moving Portless from port %d to an unprivileged port…\n", active)
			}
			if err := router.Stop(ctx); err != nil {
				return 0, fmt.Errorf("stop Portless on port %d: %w", active, err)
			}
			stillReady, checkErr := router.Ready(ctx)
			if checkErr == nil && stillReady {
				return 0, fmt.Errorf("Portless is still running on port %d; stop it before retrying", active)
			}
		}
	}
	if stored >= policy.first && stored <= policy.last {
		router := base.withPort(stored)
		ready, readyErr := router.Ready(ctx)
		if readyErr == nil && ready {
			if err := writeProxyPort(m.proxyPortPath(), stored); err != nil {
				return 0, err
			}
			return stored, nil
		}
		if readyErr != nil && !portCollision(readyErr) {
			return 0, readyErr
		}
	}
	if m.NonInteractive {
		return 0, fmt.Errorf("Portless proxy is not running; run cassie up to start it")
	}

	available := availableProxyPorts(policy.first, policy.last)
	if len(available) == 0 {
		return 0, fmt.Errorf("no Portless proxy ports are available in %d-%d", policy.first, policy.last)
	}
	available, err = shuffledPorts(available)
	if err != nil {
		return 0, err
	}
	if stored >= policy.first && stored <= policy.last {
		available = preferPort(available, stored)
	}
	limit := min(policy.attempts, len(available))
	attempted := make([]string, 0, limit)
	var lastErr error
	for _, port := range available[:limit] {
		attempted = append(attempted, strconv.Itoa(port))
		router := base.withPort(port)
		if err := router.StartOn(ctx, port); err != nil {
			lastErr = err
			if portCollision(err) {
				continue
			}
			return 0, err
		}
		if err := writeProxyPort(m.proxyPortPath(), port); err != nil {
			_ = router.Stop(ctx)
			return 0, err
		}
		return port, nil
	}
	return 0, fmt.Errorf("could not start Portless after %d attempts on ports %s: %w", limit, strings.Join(attempted, ", "), lastErr)
}

func (p Portless) withPort(port int) Portless {
	values := make(map[string]string, len(p.Env)+1)
	for key, value := range p.Env {
		values[key] = value
	}
	values["PORTLESS_PORT"] = strconv.Itoa(port)
	p.Env = values
	return p
}

func explicitProxyPort(_ map[string]string) (int, bool, error) {
	value, configured := os.LookupEnv("PORTLESS_PORT")
	if !configured || strings.TrimSpace(value) == "" {
		return 0, false, nil
	}
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, false, fmt.Errorf("PORTLESS_PORT must be between 1 and 65535; got %q", value)
	}
	return port, true, nil
}

func availableProxyPorts(first, last int) []int {
	ports := make([]int, 0, last-first+1)
	for port := first; port <= last; port++ {
		listener, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			continue
		}
		_ = listener.Close()
		ports = append(ports, port)
	}
	return ports
}

func shuffledPorts(ports []int) ([]int, error) {
	result := append([]int(nil), ports...)
	for index := len(result) - 1; index > 0; index-- {
		choice, err := rand.Int(rand.Reader, big.NewInt(int64(index+1)))
		if err != nil {
			return nil, fmt.Errorf("randomize Portless proxy ports: %w", err)
		}
		selected := int(choice.Int64())
		result[index], result[selected] = result[selected], result[index]
	}
	return result, nil
}

func preferPort(ports []int, preferred int) []int {
	for index, port := range ports {
		if port == preferred {
			ports[0], ports[index] = ports[index], ports[0]
			break
		}
	}
	return ports
}

func portCollision(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already in use") || strings.Contains(message, "is in use") || strings.Contains(message, "eaddrinuse")
}

func (m Manager) proxyPortPath() string {
	if m.PortFile != "" {
		return m.PortFile
	}
	return filepath.Join(m.DataDir, "portless.port")
}

func readProxyPort(path string) (int, bool, error) {
	if strings.TrimSpace(path) == "" {
		return 0, false, nil
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read Portless proxy port: %w", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || port < 1 || port > 65535 {
		return 0, false, fmt.Errorf("read Portless proxy port: %s contains an invalid port", path)
	}
	return port, true, nil
}

func writeProxyPort(path string, port int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Portless state directory: %w", err)
	}
	if err := writeFile(path, []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		return fmt.Errorf("save Portless proxy port: %w", err)
	}
	return nil
}

func StoredProxyPort(path string) int {
	port, exists, err := readProxyPort(path)
	if err != nil || !exists {
		return DefaultProxyPort
	}
	return port
}
