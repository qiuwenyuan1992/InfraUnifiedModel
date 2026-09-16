package nebula

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
)

type Client interface {
	ExecuteParameter(context.Context, string, map[string]interface{}) (*nebulago.ResultSet, error)
	Close()
}

type Config struct {
	Hosts                     []nebulago.HostAddress
	Space, Username, Password string
	Timeout                   time.Duration
}

var spaceName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func ParseConfig(conf *viper.Viper) (Config, error) {
	c := Config{Timeout: 5 * time.Second}
	if conf == nil {
		return c, nil
	}
	for key, dst := range map[string]*string{"space": &c.Space, "username": &c.Username, "password": &c.Password} {
		if conf.IsSet("inventory.graph." + key) {
			value, ok := conf.Get("inventory.graph." + key).(string)
			if !ok {
				return Config{}, fmt.Errorf("inventory.graph.%s must be a string", key)
			}
			*dst = value
		}
	}
	if c.Space != "" && !spaceName.MatchString(c.Space) {
		return Config{}, errors.New("invalid inventory.graph.space")
	}
	if conf.IsSet("inventory.graph.timeout_seconds") {
		n, err := strconv.Atoi(fmt.Sprint(conf.Get("inventory.graph.timeout_seconds")))
		if err != nil || n < 1 || n > 60 {
			return Config{}, errors.New("inventory.graph.timeout_seconds must be an integer from 1 to 60")
		}
		c.Timeout = time.Duration(n) * time.Second
	}
	var hosts []string
	if conf.IsSet("inventory.graph.hosts") {
		switch v := conf.Get("inventory.graph.hosts").(type) {
		case []string:
			hosts = v
		case []interface{}:
			for _, raw := range v {
				s, ok := raw.(string)
				if !ok {
					return Config{}, errors.New("inventory.graph.hosts must contain strings")
				}
				hosts = append(hosts, s)
			}
		default:
			return Config{}, errors.New("inventory.graph.hosts must be a list")
		}
	}
	for _, address := range hosts {
		host, port, err := net.SplitHostPort(address)
		n, portErr := strconv.Atoi(port)
		if err != nil || portErr != nil || n < 1 || n > 65535 || host == "" || strings.ContainsAny(host, " \t\r\n/\\") {
			return Config{}, errors.New("invalid inventory.graph.hosts address")
		}
		c.Hosts = append(c.Hosts, nebulago.HostAddress{Host: host, Port: n})
	}
	if len(c.Hosts) != 0 && c.Space != "" && (c.Username == "" || c.Password == "") {
		return Config{}, errors.New("inventory.graph username and password are required")
	}
	return c, nil
}

type sessionPool interface {
	ExecuteWithParameter(string, map[string]interface{}) (*nebulago.ResultSet, error)
	Close()
}

type client struct {
	mu     sync.RWMutex
	pool   sessionPool
	closed bool
}

func New(c Config) (Client, error) {
	if len(c.Hosts) == 0 || !spaceName.MatchString(c.Space) || c.Timeout <= 0 || c.Timeout > 60*time.Second {
		return nil, errors.New("invalid inventory graph client configuration")
	}
	conf, err := nebulago.NewSessionPoolConf(c.Username, c.Password, c.Hosts, c.Space,
		nebulago.WithTimeOut(c.Timeout), nebulago.WithMinSize(0), nebulago.WithMaxSize(16))
	if err != nil {
		return nil, errors.New("invalid inventory graph session configuration")
	}
	pool, err := nebulago.NewSessionPool(*conf, quietLogger{})
	if err != nil {
		return nil, errors.New("inventory graph connection failed")
	}
	return &client{pool: pool}, nil
}

func (c *client) ExecuteParameter(ctx context.Context, statement string, params map[string]interface{}) (*nebulago.ResultSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.closed {
		return nil, errors.New("inventory graph client closed")
	}
	// SDK 无上下文接口；socket 超时约束每次网络读写，重试次数由 SDK 有限限定。
	// 不创建请求 goroutine；取消在调用前后检查，不能中断正在进行的 SDK 调用。
	result, err := c.pool.ExecuteWithParameter(statement, params)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return result, err
}

func (c *client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.pool.Close()
	}
}

// SDK 日志可能携带查询和服务端信息，不将其输出到应用日志。
type quietLogger struct{}

func (quietLogger) Info(string)  {}
func (quietLogger) Warn(string)  {}
func (quietLogger) Error(string) {}
func (quietLogger) Fatal(string) {}
