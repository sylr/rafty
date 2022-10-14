package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/jsm.go/natscontext"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"sylr.dev/rafty"
	"sylr.dev/rafty/discovery"
	disconats "sylr.dev/rafty/discovery/nats"
	raftyzerolog "sylr.dev/rafty/logger/zerolog"
)

var (
	optionBindAddress       string
	optionAdvertisedAddress string
	optionPort              int
	optionClusterSize       int
	optionNats              bool
	optionNatsURL           string
	optionLogCaller         bool
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
	raftyMcRaftFace.PersistentFlags().BoolVar(&optionNats, "nats", false, "Use Nats disco")
	raftyMcRaftFace.PersistentFlags().StringVar(&optionNatsURL, "nats-url", "", "Nats URL")
	raftyMcRaftFace.PersistentFlags().CountVarP(&optionVerbose, "verbose", "v", "Increase verbosity")
	raftyMcRaftFace.PersistentFlags().BoolVar(&optionLogCaller, "log-caller", false, "Log caller")
}

func main() {
	err := raftyMcRaftFace.Execute()

	if err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	consoleWriter := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "Jan 2 15:04:05.000-0700"}
	multi := zerolog.MultiLevelWriter(consoleWriter)
	logger := zerolog.New(multi).With().Timestamp().Logger()

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

	logger.Info().Msg("Starting RaftyMcRaftFace")

	hclogger := raftyzerolog.HCLogger{Logger: subLogger}
	raftylogger := raftyzerolog.RaftyLogger{Logger: subLogger}

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	var discoverer discovery.Discoverer

	if optionNats {
		var natsConn *nats.Conn
		if ctx, err := natscontext.New("", true); err != nil {
			natsConn, err = nats.Connect(optionNatsURL, nats.Name("raftymcraftface"))
			if err != nil {
				cancel()
				return fmt.Errorf("failed to connect to nats: %w", err)
			}
		} else {
			natsConn, err = ctx.Connect(nats.Name("raftymcraftface"))
			if err != nil {
				logger.Debug().Err(err).Msg("Unable to create nats conn with context")
				natsConn, err = nats.Connect(optionNatsURL, nats.Name("raftymcraftface"))
				if err != nil {
					cancel()
					return fmt.Errorf("failed to connect to nats: %w", err)
				}
			}
		}

		logger.Info().Msgf("nats servers discovered: %v", natsConn.DiscoveredServers())

		natsJSDiscoverer, err := disconats.NewNatsJSDiscoverer(
			fmt.Sprintf("%s:%d", optionAdvertisedAddress, optionPort),
			natsConn,
			disconats.Logger(&raftylogger),
			disconats.JSBucket("raftymcraftface"),
		)
		if err != nil {
			cancel()
			return err
		}

		go natsJSDiscoverer.Start(ctx)
		discoverer = natsJSDiscoverer
	} else {
		LocalDiscoverer := &LocalDiscoverer{
			advertisedAddr: optionAdvertisedAddress,
			startPort:      10000,
			clusterSize:    optionClusterSize,
			ch:             make(chan struct{}),
			interval:       time.Minute,
		}

		go LocalDiscoverer.Start(ctx)
		discoverer = LocalDiscoverer
	}

	works := &RaftyMcRaftFaceWorks[string]{
		works: []RaftyMcRaftFaceWork[string]{
			"boating", "flying", "hiking", "running", "swimming",
			"walking", "skiing", "sleeping", "eating", "drinking",
		},
		ch: make(chan struct{}),
	}

	r, err := rafty.New[string, RaftyMcRaftFaceWork[string]](
		&raftylogger,
		discoverer,
		works,
		makeWork(&logger),
		rafty.RaftListeningAddressPort[string, RaftyMcRaftFaceWork[string]](optionBindAddress, optionPort),
		rafty.RaftAdvertisedAddress[string, RaftyMcRaftFaceWork[string]](optionAdvertisedAddress),
		rafty.HCLogger[string, RaftyMcRaftFaceWork[string]](&hclogger),
	)
	if err != nil {
		cancel()
		logger.Trace().Err(err).Msg("New Rafty failed")
		return err
	}

	err = r.Start(ctx)
	if err != nil {
		cancel()
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
