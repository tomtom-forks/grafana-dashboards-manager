package main

import (
	"flag"
	"fmt"
	"github.com/bruce34/grafana-dashboards-manager/internal/utils"
	"github.com/pkg/errors"
	"os"

	"github.com/bruce34/grafana-dashboards-manager/internal/config"
	"github.com/bruce34/grafana-dashboards-manager/internal/grafana"
	"github.com/bruce34/grafana-dashboards-manager/internal/logger"
	"github.com/bruce34/grafana-dashboards-manager/internal/puller"

	"github.com/sirupsen/logrus"
)

type StacktraceHook struct {
}

func (h *StacktraceHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *StacktraceHook) Fire(e *logrus.Entry) error {
	if v, found := e.Data[logrus.ErrorKey]; found {
		if err, iserr := v.(error); iserr {
			type stackTracer interface {
				StackTrace() errors.StackTrace
			}
			if st, isst := err.(stackTracer); isst {
				stack := fmt.Sprintf("%+v", st.StackTrace())
				e.Data["stacktrace"] = stack
			}
		}
	}
	return nil
}

func main() {
	// Define this flag in the main function because else it would cause a
	// conflict with the one in the pusher.
	configFile := flag.String("config", "config.yaml", "Path to the configuration file")
	version := flag.Bool("version", false, "Print version info and exit")

	flag.Parse()

	if *version {
		fmt.Printf("BuildInfo: %v", utils.BuildInfoString())
		os.Exit(0)
	}

	// Load the logger's configuration.
	logger.LogConfig()
	logrus.SetFormatter(&logrus.TextFormatter{DisableQuote: true})
	logrus.AddHook(&StacktraceHook{})
	// Load the configuration.
	cfg, err := config.Load(*configFile)
	if err != nil {
		logrus.Panic(err)
	}

	// Tell the user which sync mode we use.
	var syncMode string
	if cfg.Git != nil {
		syncMode = "git"
	} else {
		syncMode = "simple"
	}

	logrus.WithFields(logrus.Fields{
		"sync_mode": syncMode,
	}).Info("Sync mode set")

	// Initialise the Grafana API client.
	client := grafana.NewClient(cfg.Grafana.BaseURL, cfg.Grafana.APIKey, cfg.Grafana.Username, cfg.Grafana.Password, cfg.Grafana.SkipVerify)
	// Run the puller.
	if err := puller.PullGrafanaAndCommit(client, cfg); err != nil {
		logrus.Warnf("%v\n", errors.WithStack(err))
		os.Exit(1)
	}
}
