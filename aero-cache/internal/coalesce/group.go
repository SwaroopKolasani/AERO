package coalesce

import (
	"golang.org/x/sync/singleflight"
)

type Result struct {
	StatusCode  int
	Header      map[string][]string
	Body        []byte
	ContentType string
	TokensOut   int
	OriginTier  string
}

type Group struct {
	g singleflight.Group
}

func New() *Group {
	return &Group{}
}

func (g *Group) Do(key string, fn func() (*Result, error)) (*Result, bool, error) {
	v, err, shared := g.g.Do(key, func() (any, error) {
		return fn()
	})
	if err != nil {
		return nil, shared, err
	}

	res, ok := v.(*Result)
	if !ok || res == nil {
		return nil, shared, ErrInvalidResult
	}

	return res, shared, nil
}
