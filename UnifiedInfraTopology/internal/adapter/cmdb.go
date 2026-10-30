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
	EntityType       model.EntityType
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
	cfg.EntityType = model.EntityDevice
	if c.conf.IsSet(prefix + ".entity_type") {
		value, ok := c.conf.Get(prefix + ".entity_type").(string)
		if !ok {
			return cmdbConfig{}, fmt.Errorf("%w: entity_type must be a string", ErrCMDBConfig)
		}
		cfg.EntityType = model.EntityType(strings.TrimSpace(value))
	}
	if !supportedCMDBEntity(cfg.EntityType) {
		return cmdbConfig{}, fmt.Errorf("%w: unsupported entity_type; supported: device, data_center, cabinet, interface, transformer", ErrCMDBConfig)
	}
	if _, err := c.conditionFor(source, cfg.EntityType); err != nil {
		return cmdbConfig{}, err
	}
	return cfg, nil
}

func supportedCMDBEntity(entity model.EntityType) bool {
	return entity == model.EntityDevice || entity == model.EntityDataCenter || entity == model.EntityCabinet || entity == model.EntityInterface || entity == model.EntityTransformer
}

func (c *CMDB) SelectedEntity(source model.Source) (model.EntityType, error) {
	cfg, err := c.configFor(source)
	if err != nil {
		return "", err
	}
	return cfg.EntityType, nil
}

func (c *CMDB) CoversFullSource(source model.Source, entity model.EntityType) (bool, error) {
	if _, err := c.configFor(source); err != nil {
		return false, err
	}
	condition, err := c.conditionFor(source, entity)
	if err != nil {
		return false, err
	}
	return len(condition) == 0, nil
}

func (c *CMDB) conditionFor(source model.Source, entity model.EntityType) (map[string]any, error) {
	if !supportedCMDBEntity(entity) {
		return nil, fmt.Errorf("%w: unsupported entity_type; supported: device, data_center, cabinet, interface, transformer", ErrCMDBConfig)
	}
	key := "inventory.cmdb.sources." + source.ConfigRef + "." + string(entity) + "_condition"
	if !c.conf.IsSet(key) {
		return map[string]any{}, nil
	}
	switch value := c.conf.Get(key).(type) {
	case map[string]any, map[string]string:
		return c.conf.GetStringMap(key), nil
	case map[any]any:
		for key := range value {
			if _, ok := key.(string); !ok {
				return nil, fmt.Errorf("%w: condition keys must be strings", ErrCMDBConfig)
			}
		}
		return c.conf.GetStringMap(key), nil
	default:
		return nil, fmt.Errorf("%w: condition must be an object", ErrCMDBConfig)
	}
}
