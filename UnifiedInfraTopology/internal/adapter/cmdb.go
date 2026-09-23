package adapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
)

const (
	defaultCMDBPageSize         = 20000
	defaultCMDBConcurrency      = 5
	defaultCMDBTimeoutSeconds   = 30
	defaultCMDBMaxResponseBytes = 64 << 20
)

var ErrCMDBConfig = errors.New("cmdb config error")

type cmdbConfig struct {
	BaseURL          string
	Headers          map[string]string
	Query            map[string]string
	DeviceCondition  map[string]any
	TimeoutSeconds   int
	PageSize         int
	Concurrency      int
	MaxResponseBytes int64
}

// CMDB 负责从配置引用构造来源客户端，不访问 Repository。
type CMDB struct {
	conf       *viper.Viper
	httpClient *http.Client
}

func NewCMDB(conf *viper.Viper) *CMDB { return &CMDB{conf: conf} }

func NewCMDBWithClient(conf *viper.Viper, client *http.Client) *CMDB {
	return &CMDB{conf: conf, httpClient: client}
}

func (c *CMDB) clientFor(cfg cmdbConfig) *http.Client {
	client := http.Client{}
	if c.httpClient != nil {
		client = *c.httpClient
	}
	client.Timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

func (c *CMDB) Validate(ctx context.Context, source model.Source) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := c.configFor(source)
	return err
}

func (c *CMDB) configFor(source model.Source) (cmdbConfig, error) {
	if c.conf == nil || strings.TrimSpace(source.ConfigRef) == "" {
		return cmdbConfig{}, fmt.Errorf("%w: config_ref is required", ErrCMDBConfig)
	}

	prefix := "inventory.cmdb.sources." + source.ConfigRef
	baseURL := strings.TrimSpace(c.conf.GetString(prefix + ".base_url"))
	parsedURL, err := url.Parse(baseURL)
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" || parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return cmdbConfig{}, fmt.Errorf("%w: referenced source has an invalid base_url", ErrCMDBConfig)
	}
	allowInsecureHTTP := c.conf.GetBool(prefix + ".allow_insecure_http")
	if parsedURL.Scheme == "http" && !allowInsecureHTTP {
		return cmdbConfig{}, fmt.Errorf("%w: http base_url requires allow_insecure_http", ErrCMDBConfig)
	}

	cfg := cmdbConfig{
		BaseURL:          strings.TrimRight(baseURL, "/"),
		Headers:          c.conf.GetStringMapString(prefix + ".headers"),
		Query:            c.conf.GetStringMapString(prefix + ".query"),
		DeviceCondition:  c.conf.GetStringMap(prefix + ".device_condition"),
		TimeoutSeconds:   defaultCMDBTimeoutSeconds,
		PageSize:         defaultCMDBPageSize,
		Concurrency:      defaultCMDBConcurrency,
		MaxResponseBytes: defaultCMDBMaxResponseBytes,
	}
	if c.conf.IsSet(prefix + ".timeout_seconds") {
		cfg.TimeoutSeconds = c.conf.GetInt(prefix + ".timeout_seconds")
	}
	if c.conf.IsSet(prefix + ".page_size") {
		cfg.PageSize = c.conf.GetInt(prefix + ".page_size")
	}
	if c.conf.IsSet(prefix + ".concurrency") {
		cfg.Concurrency = c.conf.GetInt(prefix + ".concurrency")
	}
	if c.conf.IsSet(prefix + ".max_response_bytes") {
		cfg.MaxResponseBytes = c.conf.GetInt64(prefix + ".max_response_bytes")
	}
	if cfg.TimeoutSeconds <= 0 || cfg.PageSize <= 0 || cfg.PageSize > defaultCMDBPageSize || cfg.Concurrency <= 0 || cfg.MaxResponseBytes <= 0 {
		return cmdbConfig{}, fmt.Errorf("%w: timeout_seconds, concurrency, and max_response_bytes must be positive; page_size must be between 1 and %d", ErrCMDBConfig, defaultCMDBPageSize)
	}
	return cfg, nil
}
