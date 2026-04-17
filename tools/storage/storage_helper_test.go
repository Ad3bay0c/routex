package storage

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// mockS3Putter records PutObject calls for write tests.
type mockS3Putter struct {
	capturedKey         string
	capturedBody        string
	capturedContentType string
	err                 error
}

func (m *mockS3Putter) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.capturedKey = aws.ToString(params.Key)
	m.capturedContentType = aws.ToString(params.ContentType)
	body, _ := io.ReadAll(params.Body)
	m.capturedBody = string(body)
	return &s3.PutObjectOutput{}, nil
}

// mockS3Getter returns pre-canned content for GetObject calls in read tests.
type mockS3Getter struct {
	content     string
	contentType string
	size        int64
	err         error
}

func (m *mockS3Getter) GetObject(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	ct := m.contentType
	if ct == "" {
		ct = "text/plain"
	}
	sz := m.size
	if sz == 0 {
		sz = int64(len(m.content))
	}
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(strings.NewReader(m.content)),
		ContentType:   aws.String(ct),
		ContentLength: aws.Int64(sz),
	}, nil
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func mustUnmarshal(t *testing.T, data json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
