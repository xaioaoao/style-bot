package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Bot    BotConfig    `mapstructure:"bot"`
	NapCat NapCatConfig `mapstructure:"napcat"`
	Gemini GeminiConfig `mapstructure:"gemini"`
	RAG    RAGConfig    `mapstructure:"rag"`
	Data   DataConfig   `mapstructure:"data"`
}

type BotConfig struct {
	TargetQQ        int64  `mapstructure:"target_qq"`
	OwnerQQ         int64  `mapstructure:"owner_qq"`
	MyName          string `mapstructure:"my_name"`
	TargetName      string `mapstructure:"target_name"`
	ReplyDelayMinMs int    `mapstructure:"reply_delay_min_ms"`
	ReplyDelayMaxMs int    `mapstructure:"reply_delay_max_ms"`
	MaxContextTurns int    `mapstructure:"max_context_turns"`
	SessionTimeoutM int    `mapstructure:"session_timeout_min"`
}

type NapCatConfig struct {
	WSURL       string `mapstructure:"ws_url"`
	AccessToken string `mapstructure:"access_token"`
}

type GeminiConfig struct {
	APIKey          string  `mapstructure:"api_key"`
	ChatModel       string  `mapstructure:"chat_model"`
	EmbeddingModel  string  `mapstructure:"embedding_model"`
	Temperature     float32 `mapstructure:"temperature"`
	MaxOutputTokens int32   `mapstructure:"max_output_tokens"`
	RPMLimit        int     `mapstructure:"rpm_limit"`
}

type RAGConfig struct {
	VectorsDir    string  `mapstructure:"vectors_dir"`
	TopK          int     `mapstructure:"top_k"`
	MinSimilarity float32 `mapstructure:"min_similarity"`
}

type DataConfig struct {
	SessionsDir string `mapstructure:"sessions_dir"`
	PersonaFile string `mapstructure:"persona_file"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// 环境变量覆盖
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		v.Set("gemini.api_key", key)
	}
	if token := os.Getenv("NAPCAT_ACCESS_TOKEN"); token != "" {
		v.Set("napcat.access_token", token)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if cfg.Gemini.APIKey == "" {
		return nil, fmt.Errorf("gemini.api_key is required (set in config or GEMINI_API_KEY env)")
	}

	return &cfg, nil
}
