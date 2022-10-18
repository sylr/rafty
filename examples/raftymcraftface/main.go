package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	consul "github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-hclog"
	"github.com/nats-io/jsm.go/natscontext"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"sylr.dev/rafty"
	discoconsul "sylr.dev/rafty/discovery/consul"
	disconats "sylr.dev/rafty/discovery/nats"
	"sylr.dev/rafty/interfaces"
	raftyzerolog "sylr.dev/rafty/logger/zerolog"
)

var (
	optionBindAddress       string
	optionAdvertisedAddress string
	optionPort              int
	optionClusterSize       int
	optionConsul            bool
	optionNats              bool
	optionNatsContext       string
	optionNatsURL           string
	optionLogCaller         bool
	optionLogRaft           bool
	optionLogConsul         bool
	optionVerbose           int
)

var raftyMcRaftFace = &cobra.Command{
	Use:          "raftymcraftface",
	SilenceUsage: true,
	RunE:         run,
}

func init() {
	raftyMcRaftFace.PersistentFlags().StringVar(&optionBindAddress, "bind-address", "0.0.0.0", "Raft Address to bind to")
	raftyMcRaftFace.PersistentFlags().StringVar(&optionAdvertisedAddress, "advertised-address", "127.0.0.1", "Raft Address to advertise on the cluster")
	raftyMcRaftFace.PersistentFlags().IntVar(&optionPort, "port", 10000, "Raft Port to bind to")
	raftyMcRaftFace.PersistentFlags().IntVar(&optionClusterSize, "cluster-size", 10, "Raft cluster size")
	raftyMcRaftFace.PersistentFlags().BoolVar(&optionConsul, "consul", false, "Use Consul disco")
	raftyMcRaftFace.PersistentFlags().BoolVar(&optionNats, "nats", false, "Use Nats disco")
	raftyMcRaftFace.PersistentFlags().StringVar(&optionNatsContext, "nats-context", "", "Choose a Nats context")
	raftyMcRaftFace.PersistentFlags().StringVar(&optionNatsURL, "nats-url", "", "Nats URL")
	raftyMcRaftFace.PersistentFlags().CountVarP(&optionVerbose, "verbose", "v", "Increase verbosity")
	raftyMcRaftFace.PersistentFlags().BoolVar(&optionLogCaller, "log-caller", false, "Log caller")
	raftyMcRaftFace.PersistentFlags().BoolVar(&optionLogRaft, "log-raft", false, "Enable logs from raft library")
	raftyMcRaftFace.PersistentFlags().BoolVar(&optionLogConsul, "log-consul", false, "Enable logs from consul library")
}

func main() {
	err := raftyMcRaftFace.Execute()

	if err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	logger, raftylogger, raftlogger, consullogger := loggers()

	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		logger.Info().Msgf("Starting RaftyMcRaftFace version=%s go=%s", buildInfo.Main.Version, runtime.Version())
	} else {
		logger.Info().Msg("Starting RaftyMcRaftFace")
	}

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	// Rafty Disco
	var err error
	var discoverer interfaces.Discoverer
	if optionNats {
		logger.Info().Msg("Using NATS KV discovery")
		if discoverer, err = makeNatsKVDiscoverer(ctx, raftylogger); err != nil {
			return err
		}
	} else if optionConsul {
		logger.Info().Msg("Using Consul service discovery")
		if discoverer, err = makeConsulServiceDiscoverer(ctx, raftylogger, consullogger); err != nil {
			return err
		}
	} else {
		logger.Info().Msg("Using local discovery")
		if discoverer, err = makeLocalDiscoverer(ctx, raftylogger); err != nil {
			return err
		}
	}

	// Rafty Foreman
	formeman := &RaftyMcRaftFaceWorks[string]{
		works: []RaftyMcRaftFaceWork[string]{
			"boating", "flying", "hiking", "running", "swimming",
			"walking", "skiing", "sleeping", "eating", "drinking",
		},
		ch: make(chan struct{}),
	}

	// Rafty
	r, err := rafty.New[string, RaftyMcRaftFaceWork[string]](
		discoverer, formeman, makeWork(&logger),
		rafty.RaftListeningAddressPort[string, RaftyMcRaftFaceWork[string]](optionBindAddress, optionPort),
		rafty.RaftAdvertisedAddress[string, RaftyMcRaftFaceWork[string]](optionAdvertisedAddress),
		rafty.Logger[string, RaftyMcRaftFaceWork[string]](raftylogger),
		rafty.HCLogger[string, RaftyMcRaftFaceWork[string]](raftlogger),
	)
	if err != nil {
		cancel()
		logger.Trace().Err(err).Msg("New Rafty failed")
		return err
	}

	err = r.Start(ctx)
	if err != nil {
		logger.Trace().Err(err).Msg("Start Rafty failed")
		return err
	}

	logger.Debug().Msg("Waiting for signal")
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interrupt

	cancel()
	<-r.Done()

	return nil
}

func loggers() (logger zerolog.Logger, raftylogger interfaces.Logger, raftlogger hclog.Logger, consullogger hclog.Logger) {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	consoleWriter := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "Jan 2 15:04:05.000-0700"}
	multi := zerolog.MultiLevelWriter(consoleWriter)
	logger = zerolog.New(multi).With().Timestamp().Logger()

	switch optionVerbose {
	case 0:
		logger = logger.With().Logger().Level(zerolog.InfoLevel)
	case 1:
		logger = logger.With().Logger().Level(zerolog.DebugLevel)
	default:
		logger = logger.With().Logger().Level(zerolog.TraceLevel)
	}

	subLogger := logger
	if optionLogCaller {
		logger = logger.With().Caller().Logger()
		subLogger = subLogger.With().CallerWithSkipFrameCount(3).Logger()
	}

	// Rafty
	raftylogger = &raftyzerolog.RaftyLogger{Logger: subLogger.With().Str("name", "rafty").Logger()}

	// Raft
	if optionLogRaft {
		raftlogger = &raftyzerolog.HCLogger{Logger: subLogger}
	} else {
		raftlogger = hclog.NewNullLogger()
	}

	// Consul
	if optionLogConsul {
		consullogger = &raftyzerolog.HCLogger{Logger: subLogger}
	} else {
		consullogger = hclog.NewNullLogger()
	}

	return logger, raftylogger, raftlogger, consullogger
}

func makeNatsKVDiscoverer(ctx context.Context, logger interfaces.Logger) (interfaces.Discoverer, error) {
	var err error
	var natsConn *nats.Conn

	if len(optionNatsURL) > 0 {
		natsConn, err = nats.Connect(optionNatsURL, nats.Name("raftymcraftface"))
		if err != nil {
			return nil, fmt.Errorf("failed to connect to nats: %w", err)
		}
	} else if ctx, err := natscontext.New(optionNatsContext, true); err == nil {
		natsConn, err = ctx.Connect(nats.Name("raftymcraftface"))
		if err != nil {
			return nil, fmt.Errorf("failed to connect to nats: %w", err)
		}
	} else {
		return nil, fmt.Errorf("unable to create nats connection")
	}

	logger.Infof("nats servers discovered: %v", natsConn.DiscoveredServers())

	natsKVDiscoverer, err := disconats.NewKVDiscoverer(
		fmt.Sprintf("%s:%d", optionAdvertisedAddress, optionPort),
		natsConn,
		disconats.Logger(logger),
		disconats.JSBucket("raftymcraftface"),
	)
	if err != nil {
		return nil, err
	}

	natsKVDiscoverer.Start(ctx)

	return natsKVDiscoverer, nil
}

func makeConsulServiceDiscoverer(ctx context.Context, logger interfaces.Logger, hclogger hclog.Logger) (interfaces.Discoverer, error) {
	config := consul.DefaultConfigWithLogger(hclogger)
	client, err := consul.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to consul: %w", err)
	}

	consulDiscoverer, err := discoconsul.NewServiceDiscoverer(
		optionAdvertisedAddress,
		optionPort,
		client,
		discoconsul.Logger(logger),
		discoconsul.HCLogger(hclogger),
		discoconsul.Name("raftymcraftface"),
		discoconsul.Tags([]string{"raftymcraftface"}),
	)

	if err != nil {
		return nil, err
	}

	err = consulDiscoverer.Start(ctx)
	if err != nil {
		return nil, err
	}

	return consulDiscoverer, nil
}

func makeLocalDiscoverer(ctx context.Context, _ interfaces.Logger) (interfaces.Discoverer, error) {
	localDiscoverer := &LocalDiscoverer{
		advertisedAddr: optionAdvertisedAddress,
		startPort:      10000,
		clusterSize:    optionClusterSize,
		ch:             make(chan struct{}),
		interval:       time.Minute,
	}

	go localDiscoverer.Start(ctx)

	return localDiscoverer, nil
}

func makeWork[T string, T2 RaftyMcRaftFaceWork[T]](logger *zerolog.Logger) func(ctx context.Context, nw T2) {
	return func(ctx context.Context, nw T2) {
		logger.Info().Int("run", 0).Msgf("I'm %s!!", nw)
		for i := 1; ; i++ {
			select {
			case <-time.After(20 * time.Second):
				logger.Info().Int("run", i).Msgf("I'm %s!!", nw)
			case <-ctx.Done():
				logger.Info().Int("run", i).Msgf("I'm NOT %s anymore :(", nw)
				return
			}
		}
	}
}
