package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type Config struct {
	DSN       string
	JWTSecret []byte
	HTTPAddr  string
}

func LoadConfig(ctx context.Context) (Config, error) {
	dsnParam := strings.TrimSpace(os.Getenv("SSM_DSN_PARAM"))
	jwtParam := strings.TrimSpace(os.Getenv("SSM_JWT_SECRET_PARAM"))

	if dsnParam == "" {
		return Config{}, errors.New("config error: SSM_DSN_PARAM is required")
	}
	if jwtParam == "" {
		return Config{}, errors.New("config error: SSM_JWT_SECRET_PARAM is required")
	}

	httpAddr := strings.TrimSpace(os.Getenv("HTTP_ADDR"))
	if httpAddr == "" {
		httpAddr = ":8080"
	}

	awsCfg, err := awscfg.LoadDefaultConfig(ctx)
	if err != nil {
		return Config{}, fmt.Errorf("aws config: %w", err)
	}

	client := ssm.NewFromConfig(awsCfg)

	dsn, err := getSSMParameter(ctx, client, dsnParam)
	if err != nil {
		return Config{}, fmt.Errorf("load dsn: %w", err)
	}

	if override := strings.TrimSpace(os.Getenv("PGHOST_OVERRIDE")); override != "" {
		dsn, err = rewritePostgresHost(dsn, override)
		if err != nil {
			return Config{}, err
		}
	}

	jwtSecret, err := getSSMParameter(ctx, client, jwtParam)
	if err != nil {
		return Config{}, fmt.Errorf("load jwt secret: %w", err)
	}
	if len(jwtSecret) < 32 {
		return Config{}, errors.New("config error: JWT secret must be at least 32 bytes")
	}

	return Config{
		DSN:       dsn,
		JWTSecret: []byte(jwtSecret),
		HTTPAddr:  httpAddr,
	}, nil
}

func getSSMParameter(ctx context.Context, client *ssm.Client, name string) (string, error) {
	decrypt := true
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(decrypt),
	})
	if err != nil {
		return "", err
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", errors.New("ssm error: parameter has no value")
	}

	value := strings.TrimSpace(*out.Parameter.Value)
	if value == "" {
		return "", errors.New("ssm error: parameter value is empty")
	}

	return value, nil
}

func rewritePostgresHost(dsn, host string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("config error: parse dsn: %w", err)
	}
	if parsed.Scheme == "" {
		return "", errors.New("config error: DSN must include a URL scheme")
	}
	if parsed.Host == "" {
		return "", errors.New("config error: DSN must include a host")
	}
	if host == "" {
		return "", errors.New("config error: PGHOST_OVERRIDE is empty")
	}

	port := parsed.Port()
	if port == "" {
		port = "5432"
	}

	parsed.Host = net.JoinHostPort(host, port)
	return parsed.String(), nil
}
