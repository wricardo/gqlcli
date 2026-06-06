package gqlcli

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-resty/resty/v2"
)

func responseMetadataFromResty(resp *resty.Response) *ResponseMetadata {
	if resp == nil {
		return nil
	}
	proto := "HTTP/1.1"
	if resp.RawResponse != nil && resp.RawResponse.Proto != "" {
		proto = resp.RawResponse.Proto
	}
	return &ResponseMetadata{
		Status:     resp.Status(),
		StatusCode: resp.StatusCode(),
		Proto:      proto,
		Headers:    resp.Header().Clone(),
	}
}

func formatResponseHeaders(meta *ResponseMetadata) string {
	if meta == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", meta.Proto, meta.Status)
	writeSortedHeaders(&b, meta.Headers)
	b.WriteByte('\n')
	return b.String()
}

func formatSelectedResponseMetadata(meta *ResponseMetadata, selectors []string) (string, error) {
	if meta == nil || len(selectors) == 0 {
		return "", nil
	}
	var lines []string
	for _, selector := range selectors {
		selector = strings.TrimSpace(selector)
		switch strings.ToLower(selector) {
		case "status-code", "status_code", "code":
			lines = append(lines, fmt.Sprintf("status-code: %d", meta.StatusCode))
		case "status":
			lines = append(lines, fmt.Sprintf("status: %s %s", meta.Proto, meta.Status))
		case "headers":
			var b strings.Builder
			writeSortedHeaders(&b, meta.Headers)
			lines = append(lines, strings.TrimRight(b.String(), "\n"))
		default:
			name, ok := strings.CutPrefix(selector, "header:")
			if !ok {
				name, ok = strings.CutPrefix(selector, "header.")
			}
			if !ok || strings.TrimSpace(name) == "" {
				return "", fmt.Errorf("unsupported --metadata %q (use status, status-code, headers, or header:Name)", selector)
			}
			canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
			lines = append(lines, fmt.Sprintf("header.%s: %s", canonical, strings.Join(meta.Headers.Values(canonical), ", ")))
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func writeSortedHeaders(b *strings.Builder, headers http.Header) {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range headers.Values(key) {
			fmt.Fprintf(b, "%s: %s\n", key, value)
		}
	}
}
