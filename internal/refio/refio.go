package refio

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/snapetech/iptvtunerr/internal/httpclient"
)

const userAgent = "IptvTunerr/1.0"

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelReadCloser) Close() error {
	err := r.ReadCloser.Close()
	if r.cancel != nil {
		r.cancel()
	}
	return err
}

func Open(ref string, timeout time.Duration) (io.ReadCloser, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("empty ref")
	}
	if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
		return os.Open(ref)
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	client := httpclient.Default()
	if timeout > 0 {
		client = httpclient.WithTimeout(timeout)
	}
	resp, err := client.Do(req)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	if cancel != nil {
		return &cancelReadCloser{ReadCloser: resp.Body, cancel: cancel}, nil
	}
	return resp.Body, nil
}

func ReadAll(ref string, timeout time.Duration) ([]byte, error) {
	r, err := Open(ref, timeout)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
