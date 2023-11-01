package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var idRE = regexp.MustCompile(`^[a-z0-9]{26}$`)

const (
	// defaults
	ModelSizeDefault     = ModelSizeBase
	TranscribeAPIDefault = TranscribeAPIWhisperCPP
	OutputFormatDefault  = OutputFormatVTT
)

type OutputFormat string

const (
	OutputFormatVTT OutputFormat = "vtt"
)

type ModelSize string

const (
	ModelSizeTiny   ModelSize = "tiny"
	ModelSizeBase             = "base"
	ModelSizeSmall            = "small"
	ModelSizeMedium           = "medium"
	ModelSizeLarge            = "large"
)

type TranscribeAPI string

const (
	TranscribeAPIWhisperCPP    = "whisper.cpp"
	TranscribeAPIOpenAIWhisper = "openai/whisper"
)

type CallTranscriberConfig struct {
	// input config
	SiteURL         string
	CallID          string
	PostID          string
	AuthToken       string
	TranscriptionID string

	// output config
	TranscribeAPI TranscribeAPI
	ModelSize     ModelSize
	OutputFormat  OutputFormat
}

func (p ModelSize) IsValid() bool {
	switch p {
	case ModelSizeTiny, ModelSizeBase, ModelSizeSmall, ModelSizeMedium, ModelSizeLarge:
		return true
	default:
		return false
	}
}

func (a TranscribeAPI) IsValid() bool {
	switch a {
	case TranscribeAPIWhisperCPP, TranscribeAPIOpenAIWhisper:
		return true
	default:
		return false
	}
}

func (cfg CallTranscriberConfig) IsValid() error {
	if cfg == (CallTranscriberConfig{}) {
		return fmt.Errorf("config cannot be empty")
	}
	if cfg.SiteURL == "" {
		return fmt.Errorf("SiteURL cannot be empty")
	}

	u, err := url.Parse(cfg.SiteURL)
	if err != nil {
		return fmt.Errorf("SiteURL parsing failed: %w", err)
	} else if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("SiteURL parsing failed: invalid scheme %q", u.Scheme)
	} else if u.Path != "" {
		return fmt.Errorf("SiteURL parsing failed: invalid path %q", u.Path)
	}

	if cfg.CallID == "" {
		return fmt.Errorf("CallID cannot be empty")
	} else if !idRE.MatchString(cfg.CallID) {
		return fmt.Errorf("CallID parsing failed")
	}

	if cfg.PostID == "" {
		return fmt.Errorf("PostID cannot be empty")
	} else if !idRE.MatchString(cfg.PostID) {
		return fmt.Errorf("PostID parsing failed")
	}

	if cfg.AuthToken == "" {
		return fmt.Errorf("AuthToken cannot be empty")
	} else if !idRE.MatchString(cfg.AuthToken) {
		return fmt.Errorf("AuthToken parsing failed")
	}

	if cfg.TranscriptionID == "" {
		return fmt.Errorf("TranscriptionID cannot be empty")
	} else if !idRE.MatchString(cfg.TranscriptionID) {
		return fmt.Errorf("TranscriptionID parsing failed")
	}

	if !cfg.TranscribeAPI.IsValid() {
		return fmt.Errorf("TranscribeAPI value is not valid")
	}
	if !cfg.ModelSize.IsValid() {
		return fmt.Errorf("ModelSize value is not valid")
	}
	if cfg.OutputFormat != OutputFormatVTT {
		return fmt.Errorf("OutputFormat value is not valid")
	}

	return nil
}

func (cfg *CallTranscriberConfig) SetDefaults() {
	if cfg.TranscribeAPI == "" {
		cfg.TranscribeAPI = TranscribeAPIDefault
	}

	if cfg.ModelSize == "" {
		cfg.ModelSize = ModelSizeDefault
	}

	if cfg.OutputFormat == "" {
		cfg.OutputFormat = OutputFormatVTT
	}
}

func (cfg CallTranscriberConfig) ToEnv() []string {
	if cfg == (CallTranscriberConfig{}) {
		return nil
	}

	return []string{
		fmt.Sprintf("SITE_URL=%s", cfg.SiteURL),
		fmt.Sprintf("CALL_ID=%s", cfg.CallID),
		fmt.Sprintf("POST_ID=%s", cfg.PostID),
		fmt.Sprintf("AUTH_TOKEN=%s", cfg.AuthToken),
		fmt.Sprintf("TRANSCRIPTION_ID=%s", cfg.TranscriptionID),
		fmt.Sprintf("TRANSCRIBE_API=%s", cfg.TranscribeAPI),
		fmt.Sprintf("MODEL_SIZE=%s", cfg.ModelSize),
		fmt.Sprintf("OUTPUT_FORMAT=%s", cfg.OutputFormat),
	}
}

func (cfg CallTranscriberConfig) ToMap() map[string]any {
	if cfg == (CallTranscriberConfig{}) {
		return nil
	}

	return map[string]any{
		"site_url":         cfg.SiteURL,
		"call_id":          cfg.CallID,
		"post_id":          cfg.PostID,
		"auth_token":       cfg.AuthToken,
		"transcription_id": cfg.TranscriptionID,
		"transcribe_api":   cfg.TranscribeAPI,
		"model_size":       cfg.ModelSize,
		"output_format":    cfg.OutputFormat,
	}
}

func (cfg *CallTranscriberConfig) FromMap(m map[string]any) *CallTranscriberConfig {
	cfg.SiteURL, _ = m["site_url"].(string)
	cfg.CallID, _ = m["call_id"].(string)
	cfg.PostID, _ = m["post_id"].(string)
	cfg.AuthToken, _ = m["auth_token"].(string)
	cfg.TranscriptionID, _ = m["transcription_id"].(string)
	if api, ok := m["transcribe_api"].(string); ok {
		cfg.TranscribeAPI = TranscribeAPI(api)
	} else {
		cfg.TranscribeAPI, _ = m["transcribe_api"].(TranscribeAPI)
	}
	if modelSize, ok := m["model_size"].(string); ok {
		cfg.ModelSize = ModelSize(modelSize)
	} else {
		cfg.ModelSize, _ = m["model_size"].(ModelSize)
	}
	if outputFormat, ok := m["output_format"].(string); ok {
		cfg.OutputFormat = OutputFormat(outputFormat)
	} else {
		cfg.OutputFormat, _ = m["output_format"].(OutputFormat)
	}
	return cfg
}

func LoadFromEnv() (CallTranscriberConfig, error) {
	var cfg CallTranscriberConfig
	cfg.SiteURL = strings.TrimSuffix(os.Getenv("SITE_URL"), "/")
	cfg.CallID = os.Getenv("CALL_ID")
	cfg.PostID = os.Getenv("POST_ID")
	cfg.AuthToken = os.Getenv("AUTH_TOKEN")
	cfg.TranscriptionID = os.Getenv("TRANSCRIPTION_ID")

	if val := os.Getenv("TRANSCRIBE_API"); val != "" {
		cfg.TranscribeAPI = TranscribeAPI(val)
	}

	if val := os.Getenv("MODEL_SIZE"); val != "" {
		cfg.ModelSize = ModelSize(val)
	}

	if val := os.Getenv("OUTPUT_FORMAT"); val != "" {
		cfg.OutputFormat = OutputFormat(val)
	}

	return cfg, nil
}
