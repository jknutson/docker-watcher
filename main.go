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

// ContainerEvent holds parameters for a Container Event
type ContainerEvent struct {
	Title       string
	Body        string
	ContainerID string
	Tags        []string
}

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

func datadogEvent(c *statsd.Client, e ContainerEvent) {
	event := statsd.NewEvent(e.Title, e.Body)
	event.AggregationKey = e.ContainerID
	event.AlertType = statsd.Error
	event.SourceTypeName = "DOCKER"
	event.Tags = e.Tags
	err := c.Event(event)
	if err != nil {
		log.Fatal(err)
	} else {
		log.Println("successfully sent event to datadog")
	}
}

func main() {
	initFlags()
	setupCloseHandler()

	if version {
		fmt.Println(buildVersion)
		return
	}

	// setup datadog client
	dogstatsdClient, err := statsd.New(statsdHost)
	if err != nil {
		log.Fatal(err)
	}

	// setup docker client
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		log.Fatal(err)
	}
	cli.NegotiateAPIVersion(ctx)

	log.Println("listening for docker events")
	msgs, errs := cli.Events(ctx, types.EventsOptions{})
	for {
		select {
		case err := <-errs:
			fmt.Printf("error: %q", err)
		case msg := <-msgs:
			if msg.Type == "container" && (msg.Action == "die" || msg.Action == "exec_die") {
				exitCode := msg.Actor.Attributes["exitCode"]
				if exitCode != "0" {
					// inspect container
					inspectResponse, err := cli.ContainerInspect(ctx, msg.Actor.ID)
					if err != nil {
						log.Fatal(err)
					}
					// debugging -- print event msg and container inspect in JSON
					if os.Getenv("DEBUG") != "" {
						var inspectJSON []byte
						var msgJSON []byte
						inspectJSON, err = json.MarshalIndent(inspectResponse, "", "  ")
						if err != nil {
							log.Fatal(err)
						}
						msgJSON, err = json.MarshalIndent(msg, "", "  ")
						if err != nil {
							log.Fatal(err)
						}
						fmt.Printf("\n%s\n%s\n", string(inspectJSON), string(msgJSON))
					}

					containerEvent := ContainerEvent{}
					containerEvent.ContainerID = msg.Actor.ID
					for key, value := range inspectResponse.Config.Labels {
						containerEvent.Tags = append(containerEvent.Tags, fmt.Sprintf("%s=%s", key, value))
					}

					fromContainer := msg.From
					containerCmd := strings.Join(inspectResponse.Config.Cmd, " ")

					containerEvent.Body = fmt.Sprintf("Name: %s\n", msg.Actor.Attributes["name"])
					containerEvent.Body = fmt.Sprintf("%sID: %s\n", containerEvent.Body, msg.Actor.ID)
					containerEvent.Body = fmt.Sprintf("%sImage: %s\n", containerEvent.Body, msg.Actor.Attributes["image"])
					containerEvent.Body = fmt.Sprintf("%sCmd: %s\n", containerEvent.Body, containerCmd)
					containerEvent.Body = fmt.Sprintf("%sExit Code: %s\n", containerEvent.Body, exitCode)

					if msg.Action == "die" {
						containerEvent.Title = fmt.Sprintf("%s container exited non-zero: %s", fromContainer, exitCode)
					} else if msg.Action == "exec_die" {
						containerEvent.Title = fmt.Sprintf("%s container process exited non-zero: %s", fromContainer, exitCode)
					}

					if output == "datadog" {
						datadogEvent(dogstatsdClient, containerEvent)
					} else {
						fmt.Printf("\n%q\n", containerEvent)
					}
				}
			}
		}
	}
}
