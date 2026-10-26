package elasticsearch

import (
	"context"
	"fmt"
	"net/http"

	"github.com/elastic/go-elasticsearch/v8"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Client struct {
	es *elasticsearch.Client
}

type Config struct {
	Addresses []string
}

func NewClient(cfg Config) (*Client, error) {
	if len(cfg.Addresses) == 0 {
		return nil, fmt.Errorf("elasticsearch addresses are required")
	}

	// Wrap transport with OpenTelemetry
	transport := otelhttp.NewTransport(
		http.DefaultTransport,
		otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
			return fmt.Sprintf("es %s %s", r.Method, r.URL.Path)
		}),
	)

	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: cfg.Addresses,
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}

	// ping
	_, err = es.Info(es.Info.WithContext(context.Background()))
	if err != nil {
		return nil, err
	}

	return &Client{es: es}, nil
}

func (c *Client) Raw() *elasticsearch.Client {
	return c.es
}

// Ready performs a bounded dependency check suitable for readiness probes.
func (c *Client) Ready(ctx context.Context) error {
	if c == nil || c.es == nil {
		return fmt.Errorf("elasticsearch client is not initialized")
	}
	res, err := c.es.Info(c.es.Info.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch info returned %s", res.Status())
	}
	return nil
}
