// Package config 负责从环境变量加载 llm-hub 运行配置，全部使用 Go 标准库。
package config

import (
	"crypto/rand"
	"encoding/base64"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 汇总所有运行参数，每个字段都有默认值，缺失环境变量时使用默认值。
type Config struct {
	ServerPort             int
	ServerBaseURL          string
	JWTSecret              string
	JWTTTL                 time.Duration
	MasterKeyB64           string
	DefaultTimeout         time.Duration
	MaxFailover            int
	CircuitCooldown        time.Duration
	ModelSyncCron          string
	HealthCheckInterval    time.Duration
	ProbeEnabled           bool
	ProbeMaxTokens         int
	LogFullBody            bool
	LogMaskKeys            bool
	ContributorRewardRate  float64
	RegistrationOpen       bool
	CORSOrigins            string
	RateRPMDefault         int // F9：全局默认 RPM；0 = 不限
	RateTPMDefault         int // F9：全局默认 TPM；0 = 不限
	FirewallBurstThreshold int // rate_burst 攻击检测阈值（每 10s 窗口请求数），放宽避免误伤正常高频调用
	BodyLimitMB            int
	DataDir                string
	RealModelsPath         string
	AdminEmail             string
	AdminPassword          string
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getint(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getfloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	}
	return def
}

func getbool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

// randomSecret 在环境变量缺失时生成一次性随机密钥，绝不回退到源码内可预测的常量。
func randomSecret(key string, n int) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("无法生成 " + key + " 的随机密钥: " + err.Error())
	}
	log.Printf("WARN: %s 未配置，已生成临时随机密钥（重启后失效）", key)
	return base64.RawStdEncoding.EncodeToString(b)
}

// clampRate 把贡献奖励比例约束在 [0,1]，拒绝负数与非有限值。
func clampRate(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// getintBounded 把整型配置约束在 [min,max]，拒绝用负数/溢出值关闭安全限制。
func getintBounded(key string, def, min, max int) int {
	n := getint(key, def)
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// Load 读取环境变量并返回带默认值的配置。
func Load() *Config {
	return &Config{
		ServerPort:            getint("SERVER_PORT", 8080),
		ServerBaseURL:         getenv("SERVER_BASE_URL", "http://127.0.0.1:8080"),
		JWTSecret:             randomSecret("JWT_SECRET", 32),
		JWTTTL:                time.Duration(getint("JWT_TTL_MIN", 1440)) * time.Minute,
		MasterKeyB64:          getenv("MASTER_KEY", ""),
		DefaultTimeout:        time.Duration(getintBounded("DEFAULT_TIMEOUT_SEC", 180, 1, 3600)) * time.Second,
		MaxFailover:           getintBounded("MAX_FAILOVER", 3, 0, 10),
		CircuitCooldown:       time.Duration(getint("CIRCUIT_COOLDOWN_SEC", 30)) * time.Second,
		ModelSyncCron:         getenv("MODEL_SYNC_CRON", "0 */6 * * *"),
		HealthCheckInterval:   time.Duration(getint("HEALTH_CHECK_INTERVAL_SEC", 60)) * time.Second,
		ProbeEnabled:          getbool("PROBE_ENABLED", true),
		ProbeMaxTokens:        getintBounded("PROBE_MAX_TOKENS", 16, 0, 4096),
		LogFullBody:           getbool("LOG_FULL_BODY", true),
		LogMaskKeys:           getbool("LOG_MASK_KEYS", true),
		ContributorRewardRate: clampRate(getfloat("CONTRIBUTOR_REWARD_RATE", 0.15)),
		RegistrationOpen:      getbool("REGISTRATION_OPEN", true),
		CORSOrigins:           getenv("CORS_ORIGINS", "*"),
		// F9：全局默认额度；0 = 该维度不限。令牌未显式设定 rpm/tpm 时回落到这里。
		RateRPMDefault: getintBounded("RATE_RPM_DEFAULT", 0, 0, 1000000), // 天衡：0 = 不限
		RateTPMDefault: getintBounded("RATE_TPM_DEFAULT", 0, 0, 1000000000), // 天衡：0 = 不限
		// rate_burst 仍作为异常洪泛攻击检测保留；阈值放宽，正常高频调用不会被误判。
		FirewallBurstThreshold: getintBounded("FIREWALL_BURST_THRESHOLD", 5000, 1, 100000000),
		BodyLimitMB:            getintBounded("BODY_LIMIT_MB", 20, 1, 1024),
		DataDir:                getenv("DATA_DIR", ""),
		RealModelsPath:         getenv("REAL_MODELS_PATH", ""),
		AdminEmail:             getenv("ADMIN_EMAIL", ""),
		AdminPassword:          getenv("ADMIN_PASSWORD", ""),
	}
}

// BodyLimitBytes 返回请求体上限字节数。
func (c *Config) BodyLimitBytes() int64 {
	if c.BodyLimitMB <= 0 {
		return 20 << 20
	}
	return int64(c.BodyLimitMB) << 20
}
