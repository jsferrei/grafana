package heroic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/components/simplejson"
	"net/http"
	"net/url"

	"github.com/grafana/grafana/pkg/components/null"
	"github.com/grafana/grafana/pkg/log"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/tsdb"
	"golang.org/x/net/context/ctxhttp"
)

var (
	legendFormat = regexp.MustCompile(`\[\[(\w+?)*\]\]*|\$\s*(\w+?)*`)
)

var (
	resolutionConverter = regexp.MustCompile(`^([\d]*)(d|w|M|y)$`)
)

type HeroicExecutor struct {
	Transport *http.Transport
}

func NewHeroicExecutor(dsInfo *models.DataSource) (tsdb.TsdbQueryEndpoint, error) {
	transport, err := dsInfo.GetHttpTransport()
	if err != nil {
		return nil, err
	}

	return &HeroicExecutor{
		Transport: transport,
	}, nil
}

var (
	plog log.Logger
)

func init() {
	plog = log.New("tsdb.heroic")
	tsdb.RegisterTsdbQueryEndpoint("heroic-grafana-datasource", NewHeroicExecutor)
}

func (e *HeroicExecutor) parseTags(model *simplejson.Json) *simplejson.Json {
	var result []interface{}
	result = []interface{}{"and"}

	for _, t := range model.MustArray() {
		tagClause := simplejson.NewFromAny(t)
		var filterClause []interface{}
		if tagClause.Get("key").MustString() == "$key" {
			filterClause = []interface{}{"key", tagClause.Get("value").MustString()}
		} else if tagClause.Get("type").MustString() == "custom" {
			filterClause = []interface{}{tagClause.Get("operator").MustString(), tagClause.Get("key").MustString()}
		} else {
			if strings.HasPrefix(tagClause.Get("operator").MustString(), "!") {
				filterClauseN := []string{tagClause.Get("operator").MustString()[1:], tagClause.Get("key").MustString(), tagClause.Get("value").MustString()} // TODO, figure out multitype
				filterClause = []interface{}{"not", filterClauseN}
			} else {
				filterClause = []interface{}{tagClause.Get("operator").MustString(), tagClause.Get("key").MustString(), tagClause.Get("value").MustString()}
			}
		}
		result = append(result, filterClause)
	}

	return simplejson.NewFromAny(result)
}

func (e *HeroicExecutor) handleAggregate(model *simplejson.Json, groupBy *Sampling) Aggregator {
	params := model.Get("params").MustStringArray()
	var of *[]string
	of = &params
	if model.Get("categoryName").MustString() == "For Each" {
		of = nil
	}

	ctype := model.Get("type").MustString()
	var aggregatorEach AggregatorEach
	if ctype != "delta" && ctype != "deltaPerSecond" && ctype != "notNegative" && ctype != "stddev" {
		aggregatorEach = AggregatorEach{Type: ctype, Sampling: groupBy}
	} else {
		aggregatorEach = AggregatorEach{Type: ctype}
	}
	aggr := Aggregator{Type: "group",
		Of:   of,
		Each: []AggregatorEach{aggregatorEach}}

	return aggr
}

func (e *HeroicExecutor) handleFilter(model *simplejson.Json) (*FilterAggregate, error) {
	emptyType := FilterAggregateOf{Type: "empty"}
	var k int
	var errParsingInt error
	paramsInterface := simplejson.NewFromAny(model.Get("params").MustArray()[0])
	k, errParsingInt = paramsInterface.Int()
	if errParsingInt != nil {
		kStr, errParsingString := paramsInterface.String()
		if errParsingString != nil {
			return nil, errParsingString
		}
		var errStrToInt error
		k, errStrToInt = strconv.Atoi(kStr)
		if errStrToInt != nil {
			return nil, errStrToInt
		}
	}

	filterAggregate := FilterAggregate{Type: model.Get("type").MustString(), K: k, Of: emptyType}
	return &filterAggregate, nil
}

func (e *HeroicExecutor) handleSelect(model *simplejson.Json, groupBy *Sampling) (*simplejson.Json, error) {
	if model.Get("categoryName").MustString() == "Filters" {
		filter, err := e.handleFilter(model)
		if err != nil {
			return nil, err
		}
		return simplejson.NewFromAny(filter), nil
	} else {
		return simplejson.NewFromAny(e.handleAggregate(model, groupBy)), nil
	}
}

func (e *HeroicExecutor) parseAggregators(model *simplejson.Json, groupBy *Sampling) (*simplejson.Json, error) {
	var aggregateList []*simplejson.Json
	for _, t := range simplejson.NewFromAny(model.MustArray()[0]).MustArray() {
		lineAfter, err := e.handleSelect(simplejson.NewFromAny(t), groupBy)
		if err != nil {
			return nil, err
		}
		aggregateList = append(aggregateList, lineAfter)
	}
	newModel := simplejson.NewFromAny(aggregateList)
	return newModel, nil
}

func (e *HeroicExecutor) convertLargeResolution(resolutionString string) string {
	matches := resolutionConverter.FindStringSubmatch(resolutionString)
	if len(matches) == 0 {
		return resolutionString
	}
	value, _ := strconv.Atoi(matches[1])
	unit := matches[2]
	var convertedResolutionString string
	switch unit {
	case "d":
		convertedResolutionString = fmt.Sprintf("%dh", value*24)
		break
	case "w":
		convertedResolutionString = fmt.Sprintf("%dh", value*24*7)
		break
	case "M":
		convertedResolutionString = fmt.Sprintf("%dh", value*24*30)
		break
	case "y":
	default:
		convertedResolutionString = fmt.Sprintf("%dh", value*24*365)
	}
	return convertedResolutionString
}

func (e *HeroicExecutor) parseGroupBy(model *simplejson.Json) (*Sampling, error) {
	valueString := simplejson.NewFromAny(simplejson.NewFromAny(model.MustArray()[0]).Get("params").MustArray()[0]).MustString()
	newObject := simplejson.New()
	newObject.SetPath([]string{"unit"}, "seconds")
	cleanValueString := e.convertLargeResolution(valueString)
	parsedInterval, err := time.ParseDuration(cleanValueString)
	if err != nil {
		return nil, err
	}
	sampling := Sampling{Unit: "seconds", Value: int64(parsedInterval.Seconds())}
	return &sampling, nil

}

func (e *HeroicExecutor) createQuery(queryModel *tsdb.Query, dsInfo *models.DataSource) (*HeroicQuery, error) {
	model := queryModel.Model
	tags := e.parseTags(model.Get("tags"))
	groupBy, err := e.parseGroupBy(model.Get("groupBy"))
	if err != nil {
		return nil, err
	}
	aggregators, err := e.parseAggregators(model.Get("select"), groupBy)
	if err != nil {
		return nil, err
	}
	var features []string
	if model.Get("globalAggregation").MustBool() {
		features = []string{"com.spotify.heroic.distributed_aggregations"}
	} else {
		features = []string{}
	}
	query := HeroicQuery{Filter: tags, Aggregators: aggregators, Alias: model.Get("alias").MustString(), Features: features}
	return &query, nil

}

func (e *HeroicExecutor) GetHttpClient(ds *models.DataSource) (*http.Client, error) {
	transport, err := ds.GetHttpTransport()

	if err != nil {
		return nil, err
	}

	return &http.Client{
		Timeout:   60 * time.Second,
		Transport: transport,
	}, nil
}

func (e *HeroicExecutor) Query(ctx context.Context, dsInfo *models.DataSource, tsdbQuery *tsdb.TsdbQuery) (*tsdb.Response, error) {

	heroicRange := HeroicRange{tsdbQuery.TimeRange.GetFromAsMsEpoch(), tsdbQuery.TimeRange.GetToAsMsEpoch(), "absolute"}
	queryList := HeroicQueryList{make(map[string]HeroicQuery), heroicRange}
	plog.Info("Sending query", "dsInfo", dsInfo, "query time range:", tsdbQuery.TimeRange)

	for i, q := range tsdbQuery.Queries {
		g, err := e.createQuery(q, dsInfo)
		if err != nil {
			return nil, err
		}
		queryList.Queries[fmt.Sprint(i)] = *g
	}

	req, err := e.createRequest(dsInfo, &queryList)
	if err != nil {
		return nil, err
	}

	httpClient, err := e.GetHttpClient(dsInfo)
	if err != nil {
		return nil, err
	}

	res, err := ctxhttp.Do(ctx, httpClient, req)
	if err != nil {
		return nil, err
	}

	qResultMap, err := e.parseResponse(res, &queryList)
	if err != nil {
		return nil, err
	}
	return &tsdb.Response{Results: qResultMap}, nil
}

func (e *HeroicExecutor) createRequest(dsInfo *models.DataSource, data *HeroicQueryList) (*http.Request, error) {
	u, _ := url.Parse(dsInfo.JsonData.Get("alertingUrl").MustString())
	u.Path = path.Join(u.Path, "query/batch")

	postData, err := json.Marshal(data)
	if err != nil {
		plog.Error("Failed marshalling data", "error", err)
		return nil, fmt.Errorf("Failed to create request. error: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, u.String(), strings.NewReader(string(postData)))
	if err != nil {
		plog.Error("Failed to create request", "error", err)
		return nil, fmt.Errorf("Failed to create request. error: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if dsInfo.BasicAuth {
		req.SetBasicAuth(dsInfo.BasicAuthUser, dsInfo.BasicAuthPassword)
	}

	return req, err
}

func formatTimeRange(input string) string {
	if input == "now" {
		return input
	}
	return strings.Replace(strings.Replace(strings.Replace(input, "now", "", -1), "m", "min", -1), "M", "mon", -1)
}

func createName(alias string, name string, tags map[string]string) string {
	if alias == "" {
		return formatNameFromTags(name, tags)
	}

	nameSegment := strings.Split(name, ".")

	result := legendFormat.ReplaceAllFunc([]byte(alias), func(in []byte) []byte {
		aliasFormat := string(in)
		aliasFormat = strings.Replace(aliasFormat, "[[", "", 1)
		aliasFormat = strings.Replace(aliasFormat, "]]", "", 1)
		aliasFormat = strings.Replace(aliasFormat, "$", "", 1)

		pos, err := strconv.Atoi(aliasFormat)
		if err == nil && len(nameSegment) >= pos {
			return []byte(nameSegment[pos])
		}

		if !strings.HasPrefix(aliasFormat, "tag_") {
			return in
		}

		tagKey := strings.Replace(aliasFormat, "tag_", "", 1)
		tagValue, exist := tags[tagKey]
		if exist {
			return []byte(tagValue)
		}

		return in
	})

	return string(result)
}

func formatNameFromTags(name string, tags map[string]string) string {
	b := new(bytes.Buffer)
	fmt.Fprintf(b, "key=\"%s\"", name)
	for key, value := range tags {
		fmt.Fprintf(b, ", %s=\"%s\"", key, value)
	}
	return b.String()
}

func (e *HeroicExecutor) parseResponse(res *http.Response, queries *HeroicQueryList) (map[string]*tsdb.QueryResult, error) {
	body, err := ioutil.ReadAll(res.Body)
	defer res.Body.Close()
	if err != nil {
		return nil, err
	}

	if res.StatusCode/100 != 2 {
		plog.Error("Request failed", "status", res.Status, "body", string(body))
		return nil, fmt.Errorf("Request failed status: %v", res.Status)
	}

	var data HeroicResponse
	err = json.Unmarshal(body, &data)
	if err != nil {
		plog.Error("Failed to unmarshal opentsdb response", "error", err, "status", res.Status, "body", string(body))
		return nil, err
	}

	queryResultMap := make(map[string]*tsdb.QueryResult)
	for k, r := range data.Results {
		currentQuery := queries.Queries[k]
		queryResult := tsdb.NewQueryResult()
		queryResult.RefId = r.QueryId

		for _, points := range r.Result {
			series := tsdb.TimeSeries{Name: createName(currentQuery.Alias, points.Name, points.Tags), Tags: points.Tags}
			for _, v := range points.Values {
				series.Points = append(series.Points, tsdb.NewTimePoint(null.FloatFrom(v[1]), v[0]))
			}
			queryResult.Series = append(queryResult.Series, &series)
		}
		queryResultMap[k] = queryResult
	}
	return queryResultMap, nil
}
