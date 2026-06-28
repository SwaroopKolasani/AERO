package key

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"lukechampine.com/blake3"
)

const DomainSeparator = "aerocache-key-v1"

type Fingerprint struct {
	Model  string         `json:"model"`
	Engine string         `json:"engine"`
	Config map[string]any `json:"config,omitempty"`
}

type BuilderConfig struct {
	Fingerprint Fingerprint
	Epoch       uint64
	Tokenizer   Tokenizer
}

type Builder struct {
	fp        Fingerprint
	epoch     uint64
	tokenizer Tokenizer
}

type Tokenizer interface {
	Tokenize(renderedPrompt string) ([]uint32, error)
}

type Material struct {
	KeyHex          string
	KeyBytes        [32]byte
	StoreKey        string
	TokenIDs        []uint32
	CanonicalParams []byte
	Fingerprint     string
	Epoch           uint64
	RenderedPrompt  string
	CanonicalBody   []byte
}

func NewBuilder(cfg BuilderConfig) (*Builder, error) {
	if cfg.Fingerprint.Model == "" {
		return nil, errors.New("fingerprint model is required")
	}
	if cfg.Fingerprint.Engine == "" {
		return nil, errors.New("fingerprint engine is required")
	}
	if cfg.Tokenizer == nil {
		return nil, errors.New("tokenizer is required")
	}

	return &Builder{
		fp:        cfg.Fingerprint,
		epoch:     cfg.Epoch,
		tokenizer: cfg.Tokenizer,
	}, nil
}

func (b *Builder) Build(body []byte) (*Material, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode request json: %w", err)
	}

	obj, ok := root.(map[string]any)
	if !ok {
		return nil, errors.New("request body must be a json object")
	}

	normalized, err := normalizeValue(obj)
	if err != nil {
		return nil, err
	}

	normalizedObj, ok := normalized.(map[string]any)
	if !ok {
		return nil, errors.New("normalized request body must be an object")
	}

	canonicalBody, err := MarshalCanonical(normalizedObj)
	if err != nil {
		return nil, fmt.Errorf("canonicalize body: %w", err)
	}

	renderedPrompt, err := RenderPrompt(normalizedObj)
	if err != nil {
		return nil, fmt.Errorf("render prompt: %w", err)
	}

	tokenIDs, err := b.tokenizer.Tokenize(renderedPrompt)
	if err != nil {
		return nil, fmt.Errorf("tokenize rendered prompt: %w", err)
	}

	paramsObj := ExtractParams(normalizedObj)

	canonicalParams, err := MarshalCanonical(paramsObj)
	if err != nil {
		return nil, fmt.Errorf("canonicalize params: %w", err)
	}

	fpBytes, fpString, err := b.canonicalFingerprint()
	if err != nil {
		return nil, err
	}

	sum := HashMaterial(fpBytes, b.epoch, tokenIDs, canonicalParams)

	keyHex := hex.EncodeToString(sum[:])
	storeKey := fmt.Sprintf("aero:%s:%d:%s", safeNamespace(fpString), b.epoch, keyHex)

	return &Material{
		KeyHex:          keyHex,
		KeyBytes:        sum,
		StoreKey:        storeKey,
		TokenIDs:        tokenIDs,
		CanonicalParams: canonicalParams,
		Fingerprint:     fpString,
		Epoch:           b.epoch,
		RenderedPrompt:  renderedPrompt,
		CanonicalBody:   canonicalBody,
	}, nil
}

func (b *Builder) canonicalFingerprint() ([]byte, string, error) {
	obj := map[string]any{
		"model":  b.fp.Model,
		"engine": b.fp.Engine,
	}

	if len(b.fp.Config) > 0 {
		normCfg, err := normalizeValue(b.fp.Config)
		if err != nil {
			return nil, "", fmt.Errorf("normalize fingerprint config: %w", err)
		}
		obj["config"] = normCfg
	}

	fpBytes, err := MarshalCanonical(obj)
	if err != nil {
		return nil, "", fmt.Errorf("canonicalize fingerprint: %w", err)
	}

	return fpBytes, string(fpBytes), nil
}

func HashMaterial(fingerprint []byte, epoch uint64, tokenIDs []uint32, canonicalParams []byte) [32]byte {
	h := blake3.New(32, nil)

	writePart(h, []byte(DomainSeparator))
	writePart(h, fingerprint)

	var epochBuf [8]byte
	binary.BigEndian.PutUint64(epochBuf[:], epoch)
	writePart(h, epochBuf[:])

	var tokBuf [4]byte
	var lenBuf [8]byte

	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(tokenIDs)))
	writePart(h, lenBuf[:])

	for _, tok := range tokenIDs {
		binary.BigEndian.PutUint32(tokBuf[:], tok)
		_, _ = h.Write(tokBuf[:])
	}

	writePart(h, canonicalParams)

	var out [32]byte
	_, _ = h.XOF().Read(out[:])
	return out
}

func writePart(h *blake3.Hasher, b []byte) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(b)))
	_, _ = h.Write(lenBuf[:])
	_, _ = h.Write(b)
}

func ExtractParams(req map[string]any) map[string]any {
	params := map[string]any{}

	keys := []string{
		"model",
		"temperature",
		"top_p",
		"top_k",
		"min_p",
		"seed",
		"max_tokens",
		"stop",
		"presence_penalty",
		"frequency_penalty",
		"repetition_penalty",
		"logit_bias",
		"response_format",
		"logprobs",
		"top_logprobs",
		"n",
		"best_of",
		"tools",
		"tool_choice",
		"stream",
		"user",
		"encoding_format",
		"dimensions",
	}

	for _, k := range keys {
		if v, ok := req[k]; ok {
			params[k] = v
		}
	}

	return params
}

func RenderPrompt(req map[string]any) (string, error) {
	if messagesRaw, ok := req["messages"]; ok {
		return renderChatMessages(messagesRaw)
	}

	if promptRaw, ok := req["prompt"]; ok {
		return renderPromptValue(promptRaw)
	}

	if inputRaw, ok := req["input"]; ok {
		return renderPromptValue(inputRaw)
	}

	return "", errors.New("request has no messages, prompt, or input")
}

func renderChatMessages(v any) (string, error) {
	messages, ok := v.([]any)
	if !ok {
		return "", errors.New("messages must be an array")
	}

	var b strings.Builder

	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("message %d must be an object", i)
		}

		role, _ := msg["role"].(string)

		b.WriteString("<|")
		b.WriteString(role)
		b.WriteString("|>\n")

		content, ok := msg["content"]
		if ok {
			rendered, err := renderContent(content)
			if err != nil {
				return "", fmt.Errorf("message %d content: %w", i, err)
			}
			b.WriteString(rendered)
		}

		if name, ok := msg["name"].(string); ok && name != "" {
			b.WriteString("\n<|name|>")
			b.WriteString(name)
		}

		if toolCallID, ok := msg["tool_call_id"].(string); ok && toolCallID != "" {
			b.WriteString("\n<|tool_call_id|>")
			b.WriteString(toolCallID)
		}

		if toolCalls, ok := msg["tool_calls"]; ok {
			toolCallsBytes, err := MarshalCanonical(toolCalls)
			if err != nil {
				return "", err
			}
			b.WriteString("\n<|tool_calls|>")
			b.Write(toolCallsBytes)
		}

		b.WriteString("\n")
	}

	b.WriteString("<|assistant|>\n")
	return b.String(), nil
}

func renderContent(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case []any:
		parts := make([]string, 0, len(x))
		for _, partRaw := range x {
			partObj, ok := partRaw.(map[string]any)
			if !ok {
				return "", errors.New("content array part must be an object")
			}

			partBytes, err := MarshalCanonical(partObj)
			if err != nil {
				return "", err
			}

			parts = append(parts, string(partBytes))
		}
		return strings.Join(parts, ""), nil
	default:
		b, err := MarshalCanonical(x)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}

func renderPromptValue(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case []any:
		b, err := MarshalCanonical(x)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		b, err := MarshalCanonical(x)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}

func normalizeValue(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v2 := range x {
			norm, err := normalizeValue(v2)
			if err != nil {
				return nil, err
			}
			out[k] = norm
		}
		applyDefaults(out)
		return out, nil

	case []any:
		out := make([]any, len(x))
		for i, v2 := range x {
			norm, err := normalizeValue(v2)
			if err != nil {
				return nil, err
			}
			out[i] = norm
		}
		return out, nil

	case json.Number:
		return normalizeNumber(x)

	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, errors.New("non-finite json number")
		}
		return normalizeFloat64(x), nil

	default:
		return x, nil
	}
}

func applyDefaults(obj map[string]any) {
	if _, ok := obj["temperature"]; !ok {
		return
	}

	if _, ok := obj["n"]; !ok {
		obj["n"] = int64(1)
	}

	if _, ok := obj["best_of"]; !ok {
		obj["best_of"] = int64(1)
	}

	if _, ok := obj["stream"]; !ok {
		obj["stream"] = false
	}
}

func normalizeNumber(n json.Number) (any, error) {
	s := n.String()

	i, err := strconv.ParseInt(s, 10, 64)
	if err == nil {
		return i, nil
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid json number %q", s)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, errors.New("non-finite json number")
	}

	return normalizeFloat64(f), nil
}

func normalizeFloat64(f float64) any {
	if math.Trunc(f) == f && f >= math.MinInt64 && f <= math.MaxInt64 {
		return int64(f)
	}
	return f
}

func MarshalCanonical(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := writeCanonical(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeCanonical(b *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
		return nil

	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return nil

	case string:
		enc, _ := json.Marshal(x)
		b.Write(enc)
		return nil

	case int:
		b.WriteString(strconv.FormatInt(int64(x), 10))
		return nil

	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
		return nil

	case uint64:
		b.WriteString(strconv.FormatUint(x, 10))
		return nil

	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return errors.New("non-finite json number")
		}
		b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
		return nil

	case []any:
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil

	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}

			encKey, _ := json.Marshal(k)
			b.Write(encKey)
			b.WriteByte(':')

			if err := writeCanonical(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
		return nil

	default:
		roundTrip, err := json.Marshal(x)
		if err != nil {
			return err
		}

		var decoded any
		dec := json.NewDecoder(bytes.NewReader(roundTrip))
		dec.UseNumber()
		if err := dec.Decode(&decoded); err != nil {
			return err
		}

		norm, err := normalizeValue(decoded)
		if err != nil {
			return err
		}

		return writeCanonical(b, norm)
	}
}

func safeNamespace(s string) string {
	sum := blake3.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
