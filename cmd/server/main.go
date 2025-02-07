// Copyright 2023 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/livekit/livekit-server/pkg/rtc"
	"github.com/livekit/livekit-server/pkg/telemetry/prometheus"
	"github.com/livekit/mediatransportutil/pkg/rtcconfig"
	"github.com/livekit/protocol/logger"

	"github.com/livekit/livekit-server/pkg/config"
	"github.com/livekit/livekit-server/pkg/routing"
	"github.com/livekit/livekit-server/pkg/service"
	"github.com/livekit/livekit-server/version"

	"encoding/base64"
	"strings"
)

var baseFlags = []cli.Flag{
	&cli.StringSliceFlag{
		Name:  "bind",
		Usage: "IP address to listen on, use flag multiple times to specify multiple addresses",
	},
	&cli.StringFlag{
		Name:  "config",
		Usage: "path to LiveKit config file",
	},
	&cli.StringFlag{
		Name:    "config-body",
		Usage:   "LiveKit config in YAML, typically passed in as an environment var in a container",
		EnvVars: []string{"LIVEKIT_CONFIG"},
	},
	&cli.StringFlag{
		Name:  "key-file",
		Usage: "path to file that contains API keys/secrets",
	},
	&cli.StringFlag{
		Name:    "keys",
		Usage:   "api keys (key: secret\\n)",
		EnvVars: []string{"LIVEKIT_KEYS"},
	},
	&cli.StringFlag{
		Name:    "region",
		Usage:   "region of the current node. Used by regionaware node selector",
		EnvVars: []string{"LIVEKIT_REGION"},
	},
	&cli.StringFlag{
		Name:    "node-ip",
		Usage:   "IP address of the current node, used to advertise to clients. Automatically determined by default",
		EnvVars: []string{"NODE_IP"},
	},
	&cli.StringFlag{
		Name:    "udp-port",
		Usage:   "UDP port(s) to use for WebRTC traffic",
		EnvVars: []string{"UDP_PORT"},
	},
	&cli.StringFlag{
		Name:    "redis-host",
		Usage:   "host (incl. port) to redis server",
		EnvVars: []string{"REDIS_HOST"},
	},
	&cli.StringFlag{
		Name:    "redis-password",
		Usage:   "password to redis",
		EnvVars: []string{"REDIS_PASSWORD"},
	},
	&cli.StringFlag{
		Name:    "turn-cert",
		Usage:   "tls cert file for TURN server",
		EnvVars: []string{"LIVEKIT_TURN_CERT"},
	},
	&cli.StringFlag{
		Name:    "turn-key",
		Usage:   "tls key file for TURN server",
		EnvVars: []string{"LIVEKIT_TURN_KEY"},
	},
	// debugging flags
	&cli.StringFlag{
		Name:  "memprofile",
		Usage: "write memory profile to `file`",
	},
	&cli.BoolFlag{
		Name:  "dev",
		Usage: "sets log-level to debug, console formatter, and /debug/pprof. insecure for production",
	},
	&cli.BoolFlag{
		Name:   "disable-strict-config",
		Usage:  "disables strict config parsing",
		Hidden: true,
	},
}

func init() {
	rand.Seed(time.Now().Unix())
}

func main() {
	defer func() {
		if rtc.Recover(logger.GetLogger()) != nil {
			os.Exit(1)
		}
	}()

	generatedFlags, err := config.GenerateCLIFlags(baseFlags, true)
	if err != nil {
		fmt.Println(err)
	}

	app := &cli.App{
		Name:        "livekit-server",
		Usage:       "High performance WebRTC server",
		Description: "run without subcommands to start the server",
		Flags:       append(baseFlags, generatedFlags...),
		Action:      startServer,
		Commands: []*cli.Command{
			{
				Name:   "generate-keys",
				Usage:  "generates an API key and secret pair",
				Action: generateKeys,
			},
			{
				Name:   "ports",
				Usage:  "print ports that server is configured to use",
				Action: printPorts,
			},
			{
				// this subcommand is deprecated, token generation is provided by CLI
				Name:   "create-join-token",
				Hidden: true,
				Usage:  "create a room join token for development use",
				Action: createToken,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "room",
						Usage:    "name of room to join",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "identity",
						Usage:    "identity of participant that holds the token",
						Required: true,
					},
					&cli.BoolFlag{
						Name:     "recorder",
						Usage:    "creates a hidden participant that can only subscribe",
						Required: false,
					},
				},
			},
			{
				Name:   "list-nodes",
				Usage:  "list all nodes",
				Action: listNodes,
			},
			{
				Name:   "help-verbose",
				Usage:  "prints app help, including all generated configuration flags",
				Action: helpVerbose,
			},
		},
		Version: version.Version,
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Println(err)
	}
}

func getConfig(c *cli.Context) (*config.Config, error) {
	configBody := c.String("config-body")
	if len(configBody) > 0 {
		decodedBytes, err := base64.StdEncoding.DecodeString(configBody)
		if err == nil {
			configBody = string(decodedBytes)
		}

		configBody = strings.ReplaceAll(configBody, "\\r\\n", "\r\n")
	}
	confString, err := getConfigString(c.String("config"), configBody)
	if err != nil {
		return nil, err
	}

	strictMode := true
	if c.Bool("disable-strict-config") {
		strictMode = false
	}

	conf, err := config.NewConfig(confString, strictMode, c, baseFlags)
	if err != nil {
		return nil, err
	}
	config.InitLoggerFromConfig(&conf.Logging)

	if c.String("config") == "" && c.String("config-body") == "" && conf.Development {
		// use single port UDP when no config is provided
		conf.RTC.UDPPort = rtcconfig.PortRange{Start: 7882}
		conf.RTC.ICEPortRangeStart = 0
		conf.RTC.ICEPortRangeEnd = 0
		logger.Infow("starting in development mode")

		if len(conf.Keys) == 0 {
			logger.Infow("no keys provided, using placeholder keys",
				"API Key", "devkey",
				"API Secret", "secret@1234567890abcdefghij@1234567890abcdefghij@1234567890abcdefghij",
			)
			conf.Keys = map[string]string{
				"devkey": "secret",
			}
			shouldMatchRTCIP := false
			// when dev mode and using shared keys, we'll bind to localhost by default
			if conf.BindAddresses == nil {
				conf.BindAddresses = []string{
					"127.0.0.1",
					"::1",
				}
			} else {
				// if non-loopback addresses are provided, then we'll match RTC IP to bind address
				// our IP discovery ignores loopback addresses
				for _, addr := range conf.BindAddresses {
					ip := net.ParseIP(addr)
					if ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
						shouldMatchRTCIP = true
					}
				}
			}
			if shouldMatchRTCIP {
				for _, bindAddr := range conf.BindAddresses {
					conf.RTC.IPs.Includes = append(conf.RTC.IPs.Includes, bindAddr+"/24")
				}
			}
		}
	}
	return conf, nil
}

func startServer(c *cli.Context) error {
	rand.Seed(time.Now().UnixNano())

	memProfile := c.String("memprofile")

	conf, err := getConfig(c)
	if err != nil {
		return err
	}

	// validate API key length
	err = conf.ValidateKeys()
	if err != nil {
		return err
	}

	if memProfile != "" {
		if f, err := os.Create(memProfile); err != nil {
			return err
		} else {
			defer func() {
				// run memory profile at termination
				runtime.GC()
				_ = pprof.WriteHeapProfile(f)
				_ = f.Close()
			}()
		}
	}

	currentNode, err := routing.NewLocalNode(conf)
	if err != nil {
		return err
	}

	prometheus.Init(currentNode.Id, currentNode.Type, conf.Environment)

	server, err := service.InitializeServer(conf, currentNode)
	if err != nil {
		return err
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		logger.Infow("exit requested, shutting down", "signal", sig)
		server.Stop(false)
	}()

	return server.Start()
}

func getConfigString(configFile string, inConfigBody string) (string, error) {
	if inConfigBody != "" || configFile == "" {
		return inConfigBody, nil
	}

	outConfigBody, err := os.ReadFile(configFile)
	if err != nil {
		return "", err
	}

	return string(outConfigBody), nil
}

////////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////////
/// export functions to C

// var internalServer *service.LivekitServer = nil

// // Start server
// //
// //export Start
// func Start(config *C.char, redis *C.char) int {
// 	c := strings.Fields(C.GoString(config))
// 	configFilePath := strings.Join(c, "")

// 	r := strings.Fields(C.GoString(redis))
// 	redisAddress := strings.Join(r, "")

// 	logger.Infow("start server with config file = %s, redis: %s", configFilePath, redisAddress)
// 	err := runServer(configFilePath, redisAddress)
// 	if err != nil {
// 		logger.Errorw("start server failed", err)
// 		return -1
// 	}

// 	return 0
// }

// // Stop server
// //
// //export Stop
// func Stop() int {
// 	if internalServer == nil {
// 		logger.Infow("internalServer is nil")
// 		return -1
// 	}

// 	logger.Infow("exit requested, shutting down")
// 	internalServer.Stop(false)

// 	return 0
// }

// func runServer(configFile string, redis string) error {
// 	rand.Seed(time.Now().UnixNano())

// 	confString, err := getConfigString(configFile, "")
// 	if err != nil {
// 		return err
// 	}

// 	strictMode := true
// 	conf, err := config.NewConfig(confString, strictMode, nil, baseFlags)
// 	if err != nil {
// 		return err
// 	}
// 	config.InitLoggerFromConfig(&conf.Logging)

// 	if len(redis) > 0 {
// 		conf.Redis.Address = redis
// 	}

// 	// validate API key length
// 	err = conf.ValidateKeys()
// 	if err != nil {
// 		return err
// 	}

// 	currentNode, err := routing.NewLocalNode(conf)
// 	if err != nil {
// 		return err
// 	}

// 	prometheus.Init(currentNode.Id, currentNode.Type, conf.Environment)

// 	server, err := service.InitializeServer(conf, currentNode)
// 	if err != nil {
// 		return err
// 	}

// 	internalServer = server

// 	return server.Start()
// }
