package main

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"streamsphere/internal/common"
)

const serviceName = "api-gateway"

type serviceTarget struct {
	name  string
	url   *url.URL
	proxy *httputil.ReverseProxy
}

type breaker struct {
	mu           sync.Mutex
	failures     int
	openUntil    time.Time
	threshold    int
	openDuration time.Duration
}

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Now().Before(b.openUntil) {
		return false
	}
	if !b.openUntil.IsZero() {
		b.failures = 0
		b.openUntil = time.Time{}
	}
	return true
}

func (b *breaker) success() {
	b.mu.Lock()
	b.failures = 0
	b.openUntil = time.Time{}
	b.mu.Unlock()
}

func (b *breaker) failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= b.threshold {
		b.openUntil = time.Now().Add(b.openDuration)
	}
}

func main() {
	port := common.Env("PORT", "8080")
	targets := map[string]*serviceTarget{}
	breakers := map[string]*breaker{}
	configs := map[string]string{
		"auth":         common.EnvURL("AUTH_SERVICE_URL", "http://localhost:8081"),
		"catalog":      common.EnvURL("CATALOG_SERVICE_URL", "http://localhost:8082"),
		"media":        common.EnvURL("MEDIA_SERVICE_URL", "http://localhost:8083"),
		"playback":     common.EnvURL("PLAYBACK_SERVICE_URL", "http://localhost:8084"),
		"social":       common.EnvURL("SOCIAL_SERVICE_URL", "http://localhost:8085"),
		"analytics":    common.EnvURL("ANALYTICS_SERVICE_URL", "http://localhost:8086"),
		"notification": common.EnvURL("NOTIFICATION_SERVICE_URL", "http://localhost:8087"),
	}
	for name, rawURL := range configs {
		state := &breaker{threshold: 3, openDuration: 15 * time.Second}
		breakers[name] = state
		parsed, err := url.Parse(rawURL)
		if err != nil {
			log.Fatalf("invalid target %s: %v", name, err)
		}
		proxy := httputil.NewSingleHostReverseProxy(parsed)
		originalDirector := proxy.Director
		proxy.Director = func(request *http.Request) {
			originalDirector(request)
			request.Host = parsed.Host
			request.Header.Set("X-Forwarded-Host", request.Host)
			request.Header.Set("X-Gateway", serviceName)
		}
		proxy.Transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 90 * time.Second,
		}
		proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, proxyErr error) {
			state.failure()
			common.Fail(writer, http.StatusBadGateway, "SERVICE_UNAVAILABLE", "El microservicio no está disponible temporalmente")
		}
		proxy.ModifyResponse = func(response *http.Response) error {
			if response.StatusCode >= 500 {
				state.failure()
			} else {
				state.success()
			}
			return nil
		}
		targets[name] = &serviceTarget{name: name, url: parsed, proxy: proxy}
	}

	metrics := common.NewMetrics(serviceName)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", common.HealthHandler(serviceName))
	mux.HandleFunc("GET /health/dependencies", func(w http.ResponseWriter, r *http.Request) {
		checkHealth(w, r, targets)
	})
	mux.HandleFunc("GET /metrics", metrics.Handler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name := routeService(r)
		if name == "" {
			common.Fail(w, http.StatusNotFound, "ROUTE_NOT_FOUND", "La ruta solicitada no existe en el Gateway")
			return
		}
		target := targets[name]
		state := breakers[name]
		if !state.allow() {
			common.Fail(w, http.StatusServiceUnavailable, "CIRCUIT_OPEN", "El servicio está temporalmente aislado. Intenta nuevamente en unos segundos.")
			return
		}
		target.proxy.ServeHTTP(w, r)
	})

	handler := common.Chain(mux,
		common.CORS(common.Env("ALLOWED_ORIGIN", "*")), common.RequestID,
		common.Recover(serviceName), metrics.Middleware, common.Logging(serviceName),
	)
	server := &http.Server{
		Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout: 120 * time.Second,
	}
	log.Printf("%s listening on :%s", serviceName, port)
	log.Fatal(server.ListenAndServe())
}

func routeService(r *http.Request) string {
	path := r.URL.Path
	if strings.HasPrefix(path, "/media/") || strings.HasPrefix(path, "/api/media/") {
		return "media"
	}
	if strings.HasPrefix(path, "/api/auth/") || strings.HasPrefix(path, "/api/users/") {
		return "auth"
	}
	if path == "/api/channels" || path == "/api/channels/me" {
		return "auth"
	}
	if strings.HasPrefix(path, "/api/channels/") {
		if strings.Contains(path, "/subscriptions") {
			return "social"
		}
		return "auth"
	}
	if strings.HasPrefix(path, "/api/playback/") {
		return "playback"
	}
	if strings.HasPrefix(path, "/api/analytics/") || strings.HasPrefix(path, "/api/history/") {
		return "analytics"
	}
	if strings.HasPrefix(path, "/api/notifications/") {
		return "notification"
	}
	if strings.HasPrefix(path, "/api/playlists") || strings.HasPrefix(path, "/api/comments/") {
		return "social"
	}
	if strings.HasPrefix(path, "/api/videos/") {
		if strings.HasSuffix(path, "/comments") || strings.HasSuffix(path, "/reactions") {
			return "social"
		}
		return "catalog"
	}
	if path == "/api/videos" {
		return "catalog"
	}
	return ""
}

func checkHealth(w http.ResponseWriter, r *http.Request, targets map[string]*serviceTarget) {
	type healthResult struct {
		Service string `json:"service"`
		Status  string `json:"status"`
		Latency int64  `json:"latencyMs"`
	}
	client := &http.Client{Timeout: 2 * time.Second}
	results := make([]healthResult, 0, len(targets))
	allUp := true
	for name, target := range targets {
		started := time.Now()
		request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, target.url.String()+"/health", nil)
		response, err := client.Do(request)
		status := "UP"
		if err != nil || response.StatusCode >= 400 {
			status = "DOWN"
			allUp = false
		}
		if response != nil {
			response.Body.Close()
		}
		results = append(results, healthResult{Service: name, Status: status, Latency: time.Since(started).Milliseconds()})
	}
	statusCode := http.StatusOK
	message := "Gateway y microservicios disponibles"
	if !allUp {
		statusCode = http.StatusServiceUnavailable
		message = "Uno o más microservicios no están disponibles"
	}
	common.JSON(w, statusCode, common.APIResponse{Success: allUp, Message: message, Data: map[string]interface{}{"service": serviceName, "services": results}})
}

var _ = json.Valid
var _ = errors.Is
