package key

type Renderer interface {
	Render(req map[string]any) (string, error)
	Name() string
}

type LegacyRenderer struct{}

func (LegacyRenderer) Name() string {
	return "legacy"
}

func (LegacyRenderer) Render(req map[string]any) (string, error) {
	return RenderPrompt(req)
}
