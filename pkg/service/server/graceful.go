package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
)

type GracefulStopper struct {
	log *slog.Logger

	servers []server
	doneAny chan struct{}

	mx sync.Mutex
}

type server struct {
	name       string
	shutdownFn shutdownFunc
	ch         chan struct{}
}

type shutdownFunc func(ctx context.Context) error

func NewGracefulStopper(log *slog.Logger) *GracefulStopper {
	return &GracefulStopper{ //nolint:exhaustruct // zero value initialization
		log:     log,
		doneAny: make(chan struct{}, 1),
	}
}

type Service interface {
	Run()
	Shutdown(ctx context.Context) error
}

func (g *GracefulStopper) start(name string, run func(), shutdown func(context.Context) error) {
	g.mx.Lock()
	defer g.mx.Unlock()

	if run == nil {
		run = func() {}
	}
	if shutdown == nil {
		shutdown = func(_ context.Context) error { return nil }
	}

	srv := server{name, shutdown, make(chan struct{})}
	g.servers = append(g.servers, srv)

	go func() {
		defer close(srv.ch)

		run()

		select {
		case g.doneAny <- struct{}{}:
		default:
		}
	}()
}

func (g *GracefulStopper) StartHTTP(name string, httpSrv *http.Server) {
	g.start(name, func() {
		err := httpSrv.ListenAndServe()
		if err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				g.log.Error("HTTP server stopped with an error", slog.String("name", name), slog.Any("error", err))
			}
		}
	}, httpSrv.Shutdown)

	g.log.Info("HTTP server is listening", slog.String("name", name), slog.String("addr", httpSrv.Addr))
}

func (g *GracefulStopper) Start(name string, s Service) {
	g.start(name, s.Run, s.Shutdown)

	g.log.Info("Worker is running", slog.String("name", name))
}

func (g *GracefulStopper) StartCustom(name string, run func(), shutdown func(context.Context) error) {
	g.start(name, run, shutdown)

	g.log.Info("Worker is running", slog.String("name", name))
}

func (g *GracefulStopper) DoneAny() <-chan struct{} {
	return g.doneAny
}

func (g *GracefulStopper) ShutdownAll(ctx context.Context) {
	g.mx.Lock()
	defer g.mx.Unlock()

	var wg sync.WaitGroup
	for _, srv := range g.servers {
		wg.Add(1)
		go func(s server) {
			defer wg.Done()

			log := g.log.With(slog.String("name", s.name))

			err := s.shutdownFn(ctx)
			switch {
			case err == nil:
				log.Info("Server is shut down")
			case errors.Is(err, http.ErrServerClosed):
				log.Info("Server is already closed")
			case errors.Is(err, context.Canceled):
				log.Error("Server's shutdown stopped by context cancelation")
			case errors.Is(err, context.DeadlineExceeded):
				log.Error("Server's shutdown stopped by context deadline exceeding")
			default:
				log.Error("Server shut down with an error", slog.Any("error", err))
			}
		}(srv)
	}

	wg.Wait()

	g.servers = nil
}
