package main

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/ardanlabs/conf/v3"
	"github.com/parikxxit/go_service/app/business/web/v1/debug"
	"github.com/parikxxit/go_service/foundation/logger"
	"go.uber.org/zap"
)

var build = "develop"

func main() {
	log, err := logger.New("SALES-API")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer log.Sync()

	if err := run(log); err != nil {
		log.Errorw("Startup", "error", err)
		log.Sync()
		os.Exit(1)
	}
}

func run(log *zap.SugaredLogger) error {
	//GOMAXPROCS----------------------------------------------------------------------
	log.Infow("startup", "GOMAXPROCS", runtime.GOMAXPROCS(0), "BUILD", build)

	//CONFIGURATION-------------------------------------------------------------------

	cfg := struct {
		conf.Version
		Web struct {
			ReadTimeout     time.Duration `conf:"default:5s"`
			WriteTimeout    time.Duration `conf:"default:10s,mask"` //to mask the log we can use
			IdleTimeout     time.Duration `conf:"default:120s"`
			ShutdownTimeout time.Duration `conf:"default:20s"`
			APIHost         string        `conf:"default:0.0.0.0:3000"`
			DebugHost       string        `conf:"default:0.0.0.0:4000"`
		}
	}{
		Version: conf.Version{
			Build: build,
			Desc:  "Parikshit Singh",
		},
	}

	const prefix = "SALES"
	help, err := conf.Parse(prefix, &cfg)
	if err != nil {
		if errors.Is(err, conf.ErrHelpWanted) {
			fmt.Println(help)
			return nil
		}
		return fmt.Errorf("parsing config: %w", err)
	}
	// -------------------------------------------------------------------------
	// App Starting

	log.Infow("starting service", "version", build)
	defer log.Infow("shutdown complete")

	out, err := conf.String(&cfg)
	if err != nil {
		return fmt.Errorf("generating config for output: %w", err)
	}
	log.Infow("startup", "config", out)

	expvar.NewString("build").Set(build)
	// -------------------------------------------------------------------------
	// Start Debug Service

	go func() {
		log.Infow("startup", "status", "debug v1 router started", "host", cfg.Web.DebugHost)

		if err := http.ListenAndServe(cfg.Web.DebugHost, debug.Mux()); err != nil {
			log.Error("shutdown", "status", "debug v1 router closed", "host", cfg.Web.DebugHost, "msg", err)
		}
	}()

	// -------------------------------------------------------------------------
	// Start API service

	log.Infow("startup", "status", "Initailizing V1 api")

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	sig := <-shutdown
	log.Infow("shutdown", "status", "shutdown started", "signal", sig)
	defer log.Infow("shutdown", "status", "shutdown completed", "signal", sig)

	api := http.Server{
		Addr:         cfg.Web.APIHost,
		Handler:      nil,
		ReadTimeout:  cfg.Web.ReadTimeout,
		WriteTimeout: cfg.Web.WriteTimeout,
		IdleTimeout:  cfg.Web.IdleTimeout,
		ErrorLog:     zap.NewStdLog(log.Desugar()),
	}

	serverError := make(chan error, 1)

	go func() {
		log.Infow("startup", "status", "api router status", "host", api.Addr)
		serverError <- api.ListenAndServe()
	}()

	select {
	case err := <-serverError:
		return fmt.Errorf("Server errro: %v", err)
	case sig := <-shutdown:
		log.Infow("shutdown", "status", "shutdown started", "signal", sig)
		defer log.Infow("shutdown", "status", "shutdown completed", "signal", sig)

		ctx, cancel := context.WithTimeout(context.Background(), cfg.Web.ShutdownTimeout)
		defer cancel()

		if err := api.Shutdown(ctx); err != nil {
			api.Close()
			return fmt.Errorf("Could not stop server gracefully: %w", err)
		}
	}

	return nil
}
