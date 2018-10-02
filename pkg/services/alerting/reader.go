package alerting

import (
	"sync"
	"time"

	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/log"
	"github.com/grafana/grafana/pkg/metrics"
	m "github.com/grafana/grafana/pkg/models"
)

type RuleReader interface {
	Fetch() []*Rule
}

type DefaultRuleReader struct {
	sync.RWMutex
	//serverID       string
	serverPosition int
	clusterSize    int
	log            log.Logger
	ruleCache      []*Rule
}

func NewRuleReader() *DefaultRuleReader {
	ruleReader := &DefaultRuleReader{
		log: log.New("alerting.ruleReader"),
	}

	go ruleReader.initReader()
	return ruleReader
}

func (arr *DefaultRuleReader) initReader() {
	heartbeat := time.NewTicker(time.Second * 10)

	for range heartbeat.C {
		arr.heartbeat()
	}
}

func (arr *DefaultRuleReader) Fetch() []*Rule {
	cmd := &m.GetAllAlertsQuery{}

	if err := bus.Dispatch(cmd); err != nil {
		if arr.ruleCache != nil && len(arr.ruleCache) > 0 {
			arr.log.Warn("Error connecting to database. Using cached alert rules.", "error", err)
			return arr.ruleCache
		}
		arr.log.Error("Could not load alerts", "error", err)
		return []*Rule{}
	}

	res := make([]*Rule, 0)
	for _, ruleDef := range cmd.Result {
		if model, err := NewRuleFromDBAlert(ruleDef); err != nil {
			arr.log.Error("Could not build alert model for rule", "ruleId", ruleDef.Id, "error", err)
		} else {
			res = append(res, model)
		}
	}

	if len(res) > 0 {
		arr.ruleCache = res
	}

	metrics.M_Alerting_Active_Alerts.Set(float64(len(res)))
	return res
}

func (arr *DefaultRuleReader) heartbeat() {
	arr.clusterSize = 1
	arr.serverPosition = 1
}
