package server

import (
	"net/http"
	"sync"
)

type Readiness struct {
	Headers    http.Header
	StatusCode int
	Body       []byte

	mx sync.Mutex
}

func NewReadiness(hs http.Header, status int, body []byte) *Readiness {
	return &Readiness{Headers: hs, StatusCode: status, Body: body} //nolint:exhaustruct // mutex is initialized by zero value
}

func (r *Readiness) Set(hs http.Header, status int, body []byte) {
	r.mx.Lock()
	defer r.mx.Unlock()

	r.Headers = hs
	r.StatusCode = status
	r.Body = body
}

func (r *Readiness) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	r.mx.Lock()
	defer r.mx.Unlock()

	for k, v := range r.Headers {
		w.Header()[k] = v
	}
	w.WriteHeader(r.StatusCode)
	if len(r.Body) > 0 {
		w.Write(r.Body) //nolint:errcheck // body is optional
	}
}
