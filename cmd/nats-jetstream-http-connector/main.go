package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/glassflow/nats-jetstream-http-connector/pkg/service"
)

//nolint:govet // General config of the service with focus on human readability.
type Config struct {
	NatsServer string        `env:"NATS_SERVER"`
	Consumer   string        `env:"CONSUMER"`
	AckWait    time.Duration `env:"ACKWAIT" default:"1m"`

	Topic         string `env:"TOPIC" required:""`
	HTTPEndpoint  string `env:"HTTP_ENDPOINT" required:""`
	MaxRetries    int    `env:"MAX_RETRIES" required:""`
	ContentType   string `env:"CONTENT_TYPE" required:""`
	ResponseTopic string `env:"RESPONSE_TOPIC"`
	ErrorTopic    string `env:"ERROR_TOPIC"`
	SourceName    string `env:"SOURCE_NAME" default:"KEDAConnector"`

	Concurrent int `env:"CONCURRENT" default:"1"`
}

func main() {
	service.Main[Config](mainErr)
}

func mainErr(ctx context.Context, cfg Config, log *slog.Logger, base service.Base) error {
	nc, err := nats.Connect(cfg.NatsServer)
	if err != nil {
		return fmt.Errorf("cannot connect to nats: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("error while getting jetstream context: %w", err)
	}

	conn := jetstreamConnector{
		host:          cfg.NatsServer,
		connectordata: cfg,
		jsContext:     js,
		logger:        log,
		consumer:      cfg.Consumer,
		concurrentSem: make(chan int, cfg.Concurrent),
	}

	base.AddGracefulService("consumer", func() {
		err = conn.consumeMessage(ctx)
	}, nil)

	base.ListenAndServe(nil, nil)

	if err != nil {
		return fmt.Errorf("error occurred while parsing metadata: %w", err)
	}
	return nil
}

type jetstreamConnector struct {
	host          string
	connectordata Config
	jsContext     jetstream.JetStream
	logger        *slog.Logger
	consumer      string
	concurrentSem chan int
}

func (conn jetstreamConnector) consumeMessage(ctx context.Context) error {
	log := conn.logger
	var askWait time.Duration = conn.connectordata.AckWait

	cs, err := conn.jsContext.Consumer(ctx, conn.connectordata.Topic, conn.consumer)
	if err != nil {
		log.Error("Error on new consumer (will be ignored)", slog.Any("error", err))
		jconf := jetstream.ConsumerConfig{
			Durable:       conn.consumer,
			AckPolicy:     jetstream.AckExplicitPolicy,
			FilterSubject: conn.connectordata.Topic + ".input",
			AckWait:       askWait + time.Second,
		}
		cs, err = conn.jsContext.CreateConsumer(ctx, conn.connectordata.Topic, jconf)
		if err != nil {
			return fmt.Errorf("create consumer: %w", err)
		} else {
			log.Info("New consumer is created", slog.String("topic", conn.connectordata.Topic), slog.String("consumer", conn.consumer), slog.String("filter_subject", jconf.FilterSubject))
		}
	} else {
		log.Info("Use consumer", slog.String("topic", conn.connectordata.Topic), slog.String("consumer", conn.consumer))
	}

	log.Info("Start receiving messages")

	_, err = cs.Consume(func(msg jetstream.Msg) {
		log.Info("Got a message", slog.String("message", string(msg.Data())))
		conn.concurrentSem <- 1

		log.Info("Start processing", slog.String("message", string(msg.Data())))
		go func() {
			goCtx, cancel := context.WithTimeout(ctx, askWait)
			defer cancel()

			conn.handleHTTPRequest(goCtx, msg)
			<-conn.concurrentSem
		}()
	})
	if err != nil {
		log.Debug("error occurred while parsing metadata", slog.Any("error", err))
		return err
	}

	<-ctx.Done()

	log.Info("closing connection...")

	return nil
}

func (conn jetstreamConnector) handleHTTPRequest(ctx context.Context, msg jetstream.Msg) {
	log := conn.logger
	message := string(msg.Data())

	headers := http.Header{
		"Topic":        {conn.connectordata.Topic},
		"RespTopic":    {conn.connectordata.ResponseTopic},
		"ErrorTopic":   {conn.connectordata.ErrorTopic},
		"Content-Type": {conn.connectordata.ContentType},
		"Source-Name":  {conn.connectordata.SourceName},
	}

	maps.Copy(headers, msg.Headers()) // Add and overwrite headers from Jetstream

	resp, err := HandleHTTPRequest(ctx, string(msg.Data()), headers, conn.connectordata, log)
	if err != nil {
		conn.logger.Info(err.Error())
		conn.errorHandler(err)
		return
	}

	if resp.Body != nil {
		defer resp.Body.Close()
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		conn.logger.Info(err.Error())
		conn.errorHandler(err)
		return
	}

	success := conn.responseHandler(body)
	if !success {
		return
	}

	select {
	case <-ctx.Done():
		log.Error("Context is canceled - message won't be acked", slog.String("message", message))
		return
	default:
	}

	err = msg.Ack()
	if err != nil {
		log.Info(err.Error())
		conn.errorHandler(err)
	}
	log.Info("done processing message", slog.String("message", string(body)))
}

func (conn jetstreamConnector) responseHandler(response []byte) bool {
	log := conn.logger

	if len(conn.connectordata.ResponseTopic) == 0 {
		log.Warn("Response topic not set")
		return false
	}

	_, err := conn.jsContext.Publish(context.Background(), conn.connectordata.ResponseTopic, response)
	if err != nil {
		log.Error("failed to publish response body from http request to topic",
			slog.Any("error", err),
			slog.String("topic", conn.connectordata.ResponseTopic),
			slog.String("source", conn.connectordata.SourceName),
			slog.String("http endpoint", conn.connectordata.HTTPEndpoint),
		)
		return false
	} else {
		log.Info("Response is sent", slog.String("topic", conn.connectordata.ResponseTopic), slog.String("response", string(response)))
	}
	return true
}

func (conn jetstreamConnector) errorHandler(err error) {
	log := conn.logger

	if len(conn.connectordata.ErrorTopic) == 0 {
		log.Warn("error topic not set")
		return
	}

	_, publishErr := conn.jsContext.Publish(context.Background(), conn.connectordata.ErrorTopic, []byte(err.Error()))
	if publishErr != nil {
		log.Error("failed to publish message to error topic",
			slog.Any("error", publishErr),
			slog.String("source", conn.connectordata.SourceName),
			slog.String("message", publishErr.Error()),
			slog.String("topic", conn.connectordata.ErrorTopic))
	} else {
		log.Info("Error is sent to fallback topic", slog.String("topic", conn.connectordata.ErrorTopic), slog.String("error", err.Error()))
	}
}

// HandleHTTPRequest sends message and headers data to HTTP endpoint using POST method and returns response on success or error in case of failure
func HandleHTTPRequest(ctx context.Context, message string, headers http.Header, cfg Config, log *slog.Logger) (*http.Response, error) {

	var resp *http.Response
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		// Create request
		req, err := http.NewRequestWithContext(ctx, "POST", cfg.HTTPEndpoint, strings.NewReader(message))
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP request to invoke function. http_endpoint: %v, source: %v: %w", cfg.HTTPEndpoint, cfg.SourceName, err)
		}

		// Add headers
		for key, vals := range headers {
			for _, val := range vals {
				req.Header.Add(key, val)
			}
		}

		// Make the request
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			log.Error("sending function invocation request failed",
				slog.Any("error", err),
				slog.String("http_endpoint", cfg.HTTPEndpoint),
				slog.String("source", cfg.SourceName))
			continue
		}
		if resp == nil {
			continue
		}
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Success, quit retrying
			return resp, nil
		}
	}

	if resp == nil {
		return nil, fmt.Errorf("every function invocation retry failed; final retry gave empty response. http_endpoint: %v, source: %v", cfg.HTTPEndpoint, cfg.SourceName)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 300 {
		return nil, fmt.Errorf("request returned failure: %v. http_endpoint: %v, source: %v", resp.StatusCode, cfg.HTTPEndpoint, cfg.SourceName)
	}
	return resp, nil
}
