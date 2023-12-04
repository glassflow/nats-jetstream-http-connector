package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/vkd/gowalker"
	"github.com/vkd/gowalker/config"

	"github.com/glassflow/nats-jetstream-http-connector/pkg/metrics"
	"github.com/glassflow/nats-jetstream-http-connector/pkg/service/configtypes"
	"github.com/glassflow/nats-jetstream-http-connector/pkg/service/logger"
	"github.com/glassflow/nats-jetstream-http-connector/pkg/service/server"
)

//nolint:gochecknoglobals // build variables
var (
	version string
	commit  string
)

type baseConfig[C any] struct {
	C C `walker:"embed"`

	Addr string `default:":8080"`

	ShutdownTimeout time.Duration `default:"30s"`

	Server struct {
		ReadTimeout       time.Duration
		ReadHeaderTimeout time.Duration `default:"3s"`
		WriteTimeout      time.Duration
		IdleTimeout       time.Duration `default:"5m"`
	}

	Log struct {
		Level     configtypes.LogLevel   `default:"info"`
		Handler   configtypes.LogHandler `default:"json"`
		AddSource bool                   `default:"true"`
	}

	Metrics struct {
		Enable bool   `default:"true"`
		Addr   string `default:":2112"`
	}

	Pprof struct {
		Enable bool   `default:"true"`
		Addr   string `default:":6060"`
	}
}

type Base interface {
	AddGracefulService(name string, run func(), shutdown func(context.Context) error)
	AddHTTPServer(name string, _ *http.Server)
	ListenAndServe(_ http.Handler, _ server.RouteInfoFunc)
}

func Main[C any](fn func(context.Context, C, *slog.Logger, Base) error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)

	var cfg baseConfig[C]
	err := config.Default(&cfg)
	if err != nil {
		if errors.Is(err, gowalker.ErrPrintHelp) {
			return
		}
		slog.Error("Service finished with an error - load config", slog.Any("error", err))
		os.Exit(1)
	}

	log := slog.New(logger.SlogMetrics(
		cfg.Log.Handler(os.Stdout, &slog.HandlerOptions{
			Level:       cfg.Log.Level,
			AddSource:   cfg.Log.AddSource,
			ReplaceAttr: nil,
		}),
		metrics.CounterV1(promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "slog_total",
			Help: "Counts amount of logs by level",
		}, []string{"level"})),
	)).With(
		slog.String("version", version),
		slog.String("commit_hash", commit),
		slog.String("goversion", runtime.Version()),
	)

	graceful := server.NewGracefulStopper(log.WithGroup("graceful"))

	var mainHandler http.Handler
	var mainRouteInfoFn server.RouteInfoFunc
	mainInit := make(chan struct{})
	mainErr := make(chan error, 1)

	go func() {
		err := fn(ctx, cfg.C, log, &base{graceful, func(h http.Handler, routeInfoFn server.RouteInfoFunc) {
			mainHandler = h
			mainRouteInfoFn = routeInfoFn
			close(mainInit)

			<-ctx.Done()
		}})
		if err != nil {
			mainErr <- err
		}
		cancel()
	}()

	select {
	case <-mainInit: // OK
	case err := <-mainErr: // Error
		log.Error("Service finished with an error", slog.Any("error", err))
		os.Exit(1)
	case <-ctx.Done():
		log.Error("Context is canceled - the service initialization is stopped")
		os.Exit(1)
	}

	readiness := server.NewReadiness(nil, http.StatusServiceUnavailable, nil)

	apiServerHandler := server.ResponseTimeMiddleware(
		metrics.HistogramV3(promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "response_time",
			Help:    "Response time",
			Buckets: prometheus.DefBuckets,
		}, []string{"path", "method", "status"})),
		mainRouteInfoFn,
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Debug("Request", slog.String("url", r.URL.String()))

		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
			return
		case "/ready":
			readiness.ServeHTTP(w, r)
			return
		}

		if mainHandler != nil {
			mainHandler.ServeHTTP(w, r)
		} else {
			log.Debug("Not found", slog.String("path", r.URL.Path))
			http.NotFound(w, r)
		}
	}))

	graceful.StartHTTP("api", &http.Server{ //nolint:exhaustruct // ignore optional parameters
		Addr:              cfg.Addr,
		Handler:           apiServerHandler,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	})

	metricsServerMux := http.NewServeMux()
	metricsServerMux.Handle("/metrics", promhttp.Handler())
	graceful.StartHTTP("metrics", &http.Server{ //nolint:gosec,govet,exhaustruct // internal usage only
		Addr:    cfg.Metrics.Addr,
		Handler: metricsServerMux,
	})

	if cfg.Pprof.Enable {
		pprofMux := http.NewServeMux()
		pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
		pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		graceful.StartHTTP("pprof", &http.Server{ //nolint:gosec,govet,exhaustruct // internal usage only
			Addr:    cfg.Pprof.Addr,
			Handler: pprofMux,
		})
	}

	readiness.Set(nil, http.StatusOK, nil)

	promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "build_info",
		Help: "Provide additional build information",
	}, []string{"version", "commit", "goversion"}).WithLabelValues(version, commit, runtime.Version()).Set(1)

	log.Info("The server is ready to handle requests", slog.String("addr", cfg.Addr))

	select {
	case <-ctx.Done():
		log.Info("Termination signal is received - the service will shut down")
	case <-graceful.DoneAny():
		log.Error("One of the servers stopped unexpectedly")
		cancel()
	}

	readiness.Set(nil, http.StatusServiceUnavailable, nil)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	graceful.ShutdownAll(shutdownCtx)

	log.Info("The server gracefully shut down")

	select {
	case err := <-mainErr:
		if err != nil {
			log.Error("Main function finalization finished with an error", slog.Any("error", err))
		}
	default:
	}
}

type base struct {
	graceful       *server.GracefulStopper
	listenAndServe func(h http.Handler, routeInfoFn server.RouteInfoFunc)
}

func (b *base) AddGracefulService(name string, run func(), shutdown func(context.Context) error) {
	b.graceful.StartCustom(name, run, shutdown)
}

func (b *base) AddHTTPServer(name string, s *http.Server) {
	b.graceful.StartHTTP(name, s)
}

func (b *base) ListenAndServe(h http.Handler, routeInfoFn server.RouteInfoFunc) {
	b.listenAndServe(h, routeInfoFn)
}
