package heroic

import (
	"github.com/grafana/grafana/pkg/components/simplejson"
)

type HeroicQueryList struct {
	Queries map[string]HeroicQuery `json:"queries"`
	Range   HeroicRange            `json:"range"`
}

type HeroicRange struct {
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	Type  string `json:"type"`
}

type Sampling struct {
	Unit  string `json:"unit"`
	Value int64  `json:"value"`
}

type AggregatorEach struct {
	Type     string    `json:"type"`
	Sampling *Sampling `json:"sampling,omitempty"`
}

type Aggregator struct {
	Type string           `json:"type"`
	Of   *[]string        `json:"of"`
	Each []AggregatorEach `json:"each"`
}

type FilterAggregateOf struct {
	Type string `json:"type"`
}

type FilterAggregate struct {
	Type string            `json:"type"`
	K    int               `json:"k"`
	Of   FilterAggregateOf `json:"of"`
}

type HeroicQuery struct {
	Features    []string         `json:"features"`
	Filter      *simplejson.Json `json:"filter"`
	Aggregators *simplejson.Json `json:"aggregators"`
	Alias       string           `json:"-"`
}

type HeroicResponse struct {
	Results map[string]HeroicResult `json:"results"`
}

type HeroicResult struct {
	QueryId string      `json:"queryId"`
	Range   HeroicRange `json:"range"`
	Result  []Points    `json:"result"`
}

type Value []float64

type Points struct {
	Type   string            `json:"type"`
	Hash   string            `json:"hash"`
	Values []Value           `json:"values"`
	Name   string            `json:"key"`
	Tags   map[string]string `json:"tags,omitempty"`
}
