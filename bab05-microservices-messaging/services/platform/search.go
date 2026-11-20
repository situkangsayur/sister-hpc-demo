package platform

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SearchClient adalah klien HTTP OpenSearch sederhana dengan failover antar-node:
// bila node pertama tidak menjawab, node berikutnya dicoba.
type SearchClient struct {
	Nodes []string
	HTTP  *http.Client
}

// NewSearchClient membuat klien dari daftar URL dipisah koma.
func NewSearchClient(urls string) *SearchClient {
	return &SearchClient{Nodes: strings.Split(urls, ","), HTTP: &http.Client{Timeout: 3 * time.Second}}
}

// Do mengirim request ke node pertama yang menjawab dan mengembalikan body, node, status.
func (c *SearchClient) Do(ctx context.Context, method, path string, body []byte) ([]byte, string, int, error) {
	var lastErr error
	for _, n := range c.Nodes {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(n, "/")+path, bytes.NewReader(body))
		if err != nil {
			return nil, "", 0, err
		}
		ct := "application/json"
		if strings.HasPrefix(path, "/_bulk") {
			ct = "application/x-ndjson"
		}
		req.Header.Set("Content-Type", ct)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return b, n, resp.StatusCode, nil
	}
	return nil, "", 0, fmt.Errorf("semua node opensearch gagal: %w", lastErr)
}
