package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Ad3bay0c/routex/tools"
)

func init() {
	tools.RegisterBuiltin("read_s3", func(cfg tools.ToolConfig) (tools.Tool, error) {
		bucket := cfg.Extra["bucket"]
		if bucket == "" {
			return nil, fmt.Errorf("read_s3: extra.bucket is required")
		}

		var opts []func(*config.LoadOptions) error
		if region := cfg.Extra["region"]; region != "" {
			opts = append(opts, config.WithRegion(region))
		}

		awsCfg, err := config.LoadDefaultConfig(context.Background(), opts...)
		if err != nil {
			return nil, fmt.Errorf("read_s3: load AWS config: %w", err)
		}

		return ReadS3(bucket, s3.NewFromConfig(awsCfg)), nil
	})
}

// s3Getter is the subset of *s3.Client used by ReadS3Tool.
// Allows injection of a mock in tests.
type s3Getter interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// ReadS3Tool reads text objects from a pre-configured S3 bucket.
// The bucket is fixed at construction time — the LLM only controls the key.
type ReadS3Tool struct {
	bucket   string
	client   s3Getter
	maxBytes int
}

type readS3Input struct {
	Key      string `json:"key"`
	MaxChars int    `json:"max_chars,omitempty"`
}

type readS3Output struct {
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	Content     string `json:"content"`
	SizeBytes   int    `json:"size_bytes"`
	CharCount   int    `json:"char_count"`
	ContentType string `json:"content_type"`
	Truncated   bool   `json:"truncated"`
}

// ReadS3 returns a ReadS3Tool that reads objects from the given bucket.
// Use this constructor when you manage the AWS client yourself:
//
//	awsCfg, _ := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
//	rt.RegisterTool(storage.ReadS3("my-bucket", s3.NewFromConfig(awsCfg)))
func ReadS3(bucket string, client *s3.Client) *ReadS3Tool {
	return &ReadS3Tool{bucket: bucket, client: client, maxBytes: 1024 * 1024} // 1 MB
}

func (t *ReadS3Tool) Name() string { return "read_s3" }

func (t *ReadS3Tool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Read the content of an object from S3. " +
			"Use to load documents, data files, or previously saved reports from cloud storage. " +
			"The bucket is pre-configured — you only specify the key (object path within the bucket).",
		Parameters: map[string]tools.Parameter{
			"key": {
				Type:        "string",
				Description: "S3 object key (path within the bucket). Example: 'reports/summary.md'",
				Required:    true,
			},
			"max_chars": {
				Type:        "number",
				Description: "Maximum characters to return. Default: 8000. Increase for larger objects.",
				Required:    false,
			},
		},
	}
}

func (t *ReadS3Tool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var params readS3Input
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("read_s3: invalid input: %w", err)
	}
	if params.Key == "" {
		return nil, fmt.Errorf("read_s3: key is required")
	}

	maxChars := params.MaxChars
	if maxChars <= 0 {
		maxChars = 8000
	}

	resp, err := t.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(t.bucket),
		Key:    aws.String(params.Key),
	})
	if err != nil {
		return nil, fmt.Errorf("read_s3: get object %q: %w", params.Key, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.ContentLength != nil && *resp.ContentLength > int64(t.maxBytes) {
		return nil, fmt.Errorf(
			"read_s3: object %q is %d bytes, limit is %d — use max_chars to read a portion",
			params.Key, *resp.ContentLength, t.maxBytes,
		)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read_s3: read body of %q: %w", params.Key, err)
	}

	content := string(data)
	truncated := false

	if utf8.RuneCountInString(content) > maxChars {
		runes := []rune(content)
		content = string(runes[:maxChars])
		truncated = true
	}

	contentType := "text/plain"
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}

	return json.Marshal(readS3Output{
		Bucket:      t.bucket,
		Key:         params.Key,
		Content:     content,
		SizeBytes:   len(data),
		CharCount:   utf8.RuneCountInString(content),
		ContentType: contentType,
		Truncated:   truncated,
	})
}

var _ tools.Tool = (*ReadS3Tool)(nil)
