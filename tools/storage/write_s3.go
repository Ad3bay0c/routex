package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Ad3bay0c/routex/tools"
)

func init() {
	tools.RegisterBuiltin("write_s3", func(cfg tools.ToolConfig) (tools.Tool, error) {
		bucket := cfg.Extra["bucket"]
		if bucket == "" {
			return nil, fmt.Errorf("write_s3: extra.bucket is required")
		}

		var opts []func(*config.LoadOptions) error
		if region := cfg.Extra["region"]; region != "" {
			opts = append(opts, config.WithRegion(region))
		}

		awsCfg, err := config.LoadDefaultConfig(context.Background(), opts...)
		if err != nil {
			return nil, fmt.Errorf("write_s3: load AWS config: %w", err)
		}

		return WriteS3(bucket, s3.NewFromConfig(awsCfg)), nil
	})
}

// s3Putter is the subset of *s3.Client used by WriteS3Tool.
// Allows injection of a mock in tests.
type s3Putter interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// WriteS3Tool writes text objects to a pre-configured S3 bucket.
// The bucket is fixed at construction time — the LLM only controls the key.
type WriteS3Tool struct {
	bucket string
	client s3Putter
}

type writeS3Input struct {
	Key         string `json:"key"`
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
}

type writeS3Output struct {
	Success bool   `json:"success"`
	Bucket  string `json:"bucket"`
	Key     string `json:"key"`
	Bytes   int    `json:"bytes_written"`
	URI     string `json:"uri"`
	Message string `json:"message"`
}

// WriteS3 returns a WriteS3Tool that writes objects to the given bucket.
// Use this constructor when you manage the AWS client yourself:
//
//	awsCfg, _ := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
//	rt.RegisterTool(storage.WriteS3("my-bucket", s3.NewFromConfig(awsCfg)))
func WriteS3(bucket string, client *s3.Client) *WriteS3Tool {
	return &WriteS3Tool{bucket: bucket, client: client}
}

func (t *WriteS3Tool) Name() string { return "write_s3" }

func (t *WriteS3Tool) Schema() tools.Schema {
	return tools.Schema{
		Description: "Write text content to an object in S3. " +
			"Use this to persist reports, summaries, or any output to cloud storage. " +
			"The bucket is pre-configured — you only specify the key (object path within the bucket).",
		Parameters: map[string]tools.Parameter{
			"key": {
				Type:        "string",
				Description: "S3 object key (path within the bucket). Example: 'reports/summary.md', 'outputs/2024/report.txt'",
				Required:    true,
			},
			"content": {
				Type:        "string",
				Description: "The text content to write to the object.",
				Required:    true,
			},
			"content_type": {
				Type:        "string",
				Description: "MIME type of the content. Defaults to 'text/plain'. Example: 'text/markdown', 'application/json'",
				Required:    false,
			},
		},
	}
}

func (t *WriteS3Tool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var params writeS3Input
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("write_s3: invalid input: %w", err)
	}
	if params.Key == "" {
		return nil, fmt.Errorf("write_s3: key is required")
	}
	if params.Content == "" {
		return nil, fmt.Errorf("write_s3: content is required")
	}

	contentType := params.ContentType
	if contentType == "" {
		contentType = "text/plain"
	}

	body := []byte(params.Content)

	_, err := t.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(t.bucket),
		Key:         aws.String(params.Key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return nil, fmt.Errorf("write_s3: put object %q: %w", params.Key, err)
	}

	return json.Marshal(writeS3Output{
		Success: true,
		Bucket:  t.bucket,
		Key:     params.Key,
		Bytes:   len(body),
		URI:     fmt.Sprintf("s3://%s/%s", t.bucket, params.Key),
		Message: fmt.Sprintf("Successfully wrote %d bytes to s3://%s/%s", len(body), t.bucket, params.Key),
	})
}

var _ tools.Tool = (*WriteS3Tool)(nil)
