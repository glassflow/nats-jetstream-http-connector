package server

import (
	"net/http"
	"strconv"
	"time"
)

type ResponseTimeFunc func(path, method, status string, seconds float64)

type RouteInfoFunc func(*http.Request) (pathPattern string, ok bool)

func ResponseTimeMiddleware(hist ResponseTimeFunc, routeInfoFn RouteInfoFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if routeInfoFn != nil {
				if pathPattern, ok := routeInfoFn(r); ok {
					path = pathPattern
				}
			}

			t0 := time.Now()
			srw := newStatusRW(rw)
			next.ServeHTTP(srw, r)
			hist(path, r.Method, srw.Status(), time.Since(t0).Seconds())
		})
	}
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func newStatusRW(rw http.ResponseWriter) *statusResponseWriter {
	return &statusResponseWriter{
		ResponseWriter: rw,
		status:         http.StatusOK,
	}
}

func (s *statusResponseWriter) WriteHeader(statusCode int) {
	s.status = statusCode
	s.ResponseWriter.WriteHeader(statusCode)
}

func (s *statusResponseWriter) Status() string {
	return strconv.Itoa(s.status)
}
