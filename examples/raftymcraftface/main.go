package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	optionNatsURL           string
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
	raftyMcRaftFace.PersistentFlags().StringVar(&optionNatsURL, "nats-url", "", "Nats URL")
}

func main() {
	err := raftyMcRaftFace.Execute()

	if err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "Jan 2 15:04:05.000-0700",
	}
	multi := zerolog.MultiLevelWriter(consoleWriter)
	logger := zerolog.New(multi).With().Timestamp().Logger().Level(zerolog.DebugLevel)

	logger.Info().Msg("Starting RaftyMcRaftFace")

	hclogger := raftyzerolog.HCLogger{Logger: logger}
	raftylogger := raftyzerolog.RaftyLogger{Logger: logger}

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	var discoverer discovery.Discoverer

	if len(optionNatsURL) > 0 {
		natsConn, err := nats.Connect(optionNatsURL)
		if err != nil {
			cancel()
			return fmt.Errorf("failed to connect to nats: %w", err)
		}

		natsJSContext, err := natsConn.JetStream()
		if err != nil {
			cancel()
			return fmt.Errorf("failed to get a JetStream context: %w", err)
		}

		natsJSDiscoverer, err := disconats.NewNatsJSDiscoverer(
			&raftylogger,
			fmt.Sprintf("%s:%d", optionAdvertisedAddress, optionPort),
			"rafty",
			natsJSContext,
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
		rafty.WithRaftListeningAddressPort[string, RaftyMcRaftFaceWork[string]](optionBindAddress, optionPort),
		rafty.WithRaftAdvertisedAddress[string, RaftyMcRaftFaceWork[string]](optionAdvertisedAddress),
		rafty.WithHCLogger[string, RaftyMcRaftFaceWork[string]](&hclogger),
	)
	if err != nil {
		cancel()
		return err
	}

	err = r.Start(ctx)
	if err != nil {
		cancel()
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
