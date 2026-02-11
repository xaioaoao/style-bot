package persona

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Persona struct {
	Style        StyleProfile        `json:"style"`
	Relationship RelationshipMemory  `json:"relationship"`
}

type StyleProfile struct {
	TypicalLength    string   `json:"typical_length"`
	Catchphrases     []string `json:"catchphrases"`
	EmojiPatterns    []string `json:"emoji_patterns"`
	PunctuationStyle string   `json:"punctuation_style"`
	ResponseStyle    string   `json:"response_style"`
	HumorStyle       string   `json:"humor_style"`
	Formality        string   `json:"formality"`
	MultiMessage     bool     `json:"multi_message"`
	NegativePatterns []string `json:"negative_patterns"`
	GreetingExamples []string `json:"greeting_examples"`
	AgreementExamples []string `json:"agreement_examples"`
	RefusalExamples  []string `json:"refusal_examples"`
}

type RelationshipMemory struct {
	Relationship string            `json:"relationship"`
	SharedTopics []string          `json:"shared_topics"`
	InsideJokes  []string          `json:"inside_jokes"`
	Tone         string            `json:"tone"`
	KeyFacts     map[string]string `json:"key_facts"`
}

func LoadFromFile(path string) (*Persona, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read persona file: %w", err)
	}
	var p Persona
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("unmarshal persona: %w", err)
	}
	return &p, nil
}

// FormatStyleForPrompt 将风格档案格式化为 prompt 文本
func (p *Persona) FormatStyleForPrompt() string {
	s := p.Style
	var b strings.Builder

	if s.TypicalLength != "" {
		fmt.Fprintf(&b, "- 消息长度：%s\n", s.TypicalLength)
	}
	if len(s.Catchphrases) > 0 {
		fmt.Fprintf(&b, "- 口头禅：经常说%s\n", strings.Join(quoteAll(s.Catchphrases), "、"))
	}
	if len(s.EmojiPatterns) > 0 {
		fmt.Fprintf(&b, "- 表情习惯：喜欢用%s\n", strings.Join(s.EmojiPatterns, "、"))
	}
	if s.PunctuationStyle != "" {
		fmt.Fprintf(&b, "- 标点：%s\n", s.PunctuationStyle)
	}
	if s.ResponseStyle != "" {
		fmt.Fprintf(&b, "- 语气：%s\n", s.ResponseStyle)
	}
	if s.HumorStyle != "" {
		fmt.Fprintf(&b, "- 幽默风格：%s\n", s.HumorStyle)
	}
	if s.Formality != "" {
		fmt.Fprintf(&b, "- 正式程度：%s\n", s.Formality)
	}
	if s.MultiMessage {
		b.WriteString("- 会分多条短消息发送，而不是一条长消息\n")
	}
	if len(s.NegativePatterns) > 0 {
		b.WriteString("\n你绝对不会：\n")
		for _, p := range s.NegativePatterns {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	return b.String()
}

// FormatRelationshipForPrompt 将关系记忆格式化为 prompt 文本
func (p *Persona) FormatRelationshipForPrompt(targetName string) string {
	r := p.Relationship
	var b strings.Builder

	if r.Relationship != "" {
		fmt.Fprintf(&b, "- 关系：%s\n", r.Relationship)
	}
	if len(r.SharedTopics) > 0 {
		fmt.Fprintf(&b, "- 共同话题：%s\n", strings.Join(r.SharedTopics, "、"))
	}
	if len(r.InsideJokes) > 0 {
		fmt.Fprintf(&b, "- 内部梗/共同经历：%s\n", strings.Join(r.InsideJokes, "；"))
	}
	if r.Tone != "" {
		fmt.Fprintf(&b, "- 对%s的态度：%s\n", targetName, r.Tone)
	}
	for k, v := range r.KeyFacts {
		fmt.Fprintf(&b, "- %s的%s：%s\n", targetName, k, v)
	}
	return b.String()
}

func quoteAll(ss []string) []string {
	result := make([]string, len(ss))
	for i, s := range ss {
		result[i] = "\"" + s + "\""
	}
	return result
}
