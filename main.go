package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

var (
	buildVersion string
	output       string
	statsdHost   string
	version      bool
)

func usage() {
	println(`Usage: docker-watcher [options]
Do things in response to docker container events
Options:`)
	flag.PrintDefaults()

	println(`
Environment Variables:
  DEBUG - enable debug mode. set to any non-blank string to enable. set to "pretty" for formatted JSON
`)
	println(`For more information, see REPO_URL`)
}

func initFlags() {
	statsdHost = os.Getenv("DOGSTATSD_HOST")
	if statsdHost == "" {
		statsdHost = "localhost:8125"
	}
	flag.BoolVar(&version, "version", false, "show version")
	flag.StringVar(&statsdHost, "statsd-host", statsdHost, "address:port for DataDogStatsD listener")
	flag.StringVar(&output, "output", "datadog", "where to send events")
	flag.Usage = usage
	flag.Parse()
}

func setupCloseHandler() {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println("\r- Ctrl+C pressed, exiting")
		os.Exit(0)
	}()
}

func debug(msg string) {
	if os.Getenv("DEBUG") != "" {
		log.Println(msg)
	}
}

func main() {
	initFlags()
	setupCloseHandler()

	if version {
		fmt.Println(buildVersion)
		return
	}

	debug("debug mode")

	// setup datadog client
	dogstatsdClient, err := statsd.New(statsdHost)
	if err != nil {
		log.Fatal(err)
	}
	err = dogstatsdClient.SimpleEvent("docker-watcher started", "huzzah")
	if err != nil {
		log.Fatal(err)
	}

	// setup docker client
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}
	cli.NegotiateAPIVersion(ctx)

	msgs, errs := cli.Events(ctx, types.EventsOptions{})

	log.Println("listening for docker events")
	for {
		select {
		case err := <-errs:
			fmt.Printf("error: %q", err)
		case msg := <-msgs:
			if msg.Type == "container" && (msg.Action == "die" || msg.Action == "exec_die") {
				inspectResponse, err := cli.ContainerInspect(ctx, msg.Actor.ID)
				// debugging -- print event msg and container inspect in JSON
				if os.Getenv("DEBUG") != "" {
					var inspectJSON []byte
					var msgJSON []byte
					if os.Getenv("DEBUG") == "pretty" {
						inspectJSON, err = json.MarshalIndent(inspectResponse, "", "  ")
						if err != nil {
							log.Fatal(err)
						}
						msgJSON, err = json.MarshalIndent(msg, "", "  ")
						if err != nil {
							log.Fatal(err)
						}
					} else {
						inspectJSON, err = json.Marshal(inspectResponse)
						if err != nil {
							log.Fatal(err)
						}
						msgJSON, err = json.Marshal(msg)
						if err != nil {
							log.Fatal(err)
						}
					}
					fmt.Printf("\n%s\n%s\n", string(inspectJSON), string(msgJSON))
				}

				fromContainer := msg.From
				containerCmd := strings.Join(inspectResponse.Config.Cmd, " ")
				exitCode := msg.Actor.Attributes["exitCode"]

				// TOOD: abstract this out to a struct that can pass to datadog/stdout/etc.
				var eventTitle string
				eventMessage := fmt.Sprintf("Name: %s\n", msg.Actor.Attributes["name"])
				eventMessage = fmt.Sprintf("%sID: %s\n", eventMessage, msg.Actor.ID)
				eventMessage = fmt.Sprintf("%sImage: %s\n", eventMessage, msg.Actor.Attributes["image"])
				eventMessage = fmt.Sprintf("%sCmd: %s\n", eventMessage, containerCmd)
				eventMessage = fmt.Sprintf("%sExit Code: %s\n", eventMessage, exitCode)

				containerLabels := []string{}
				for key, value := range inspectResponse.Config.Labels {
					containerLabels = append(containerLabels, fmt.Sprintf("%s=%s", key, value))
				}

				if msg.Action == "die" {
					if output == "datadog" {
						eventTitle = fmt.Sprintf("%s container process exited non-zero: %s", fromContainer, exitCode)
						event := statsd.NewEvent(eventTitle, eventMessage)
						event.AggregationKey = msg.Actor.ID
						event.AlertType = statsd.Error
						event.SourceTypeName = "DOCKER"
						event.Tags = containerLabels
						err = dogstatsdClient.Event(event)
						if err != nil {
							log.Fatal(err)
						} else {
							log.Println("successfully sent event to datadog")
							debug(eventMessage)
						}
					} else {
						if exitCode != "0" {
							fmt.Printf("container exited non-zero: %s (%s)\texit code: %s", msg.From, msg.Actor.ID, msg.Actor.Attributes["exitCode"])
						}
					}
				} else if msg.Action == "exec_die" {
					if inspectResponse.State.Status == "unhealthy" {
						// eventTitle := fmt.Sprintf("%s container is unhealthy", fromContainer)
						//  event := statsd.NewEvent(eventTitle, eventMessage)
						// container is unhealthy
						debug("nil")
					}
				}
			}
		}
	}
}
