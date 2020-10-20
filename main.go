package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/template"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
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
	ContainerID, ContainerName, Image string
	Title, Body, Cmd, ExitCode        string
	Action                            string
	Tags                              []string
	EventMessage                      events.Message
}

const datadogEventTemplate = `Name: {{.ContainerName}}
Image: {{.Image}}
Exit Code: {{.ExitCode}}
ID: {{.ContainerID}}
`

const stdoutEventTemplate = `{{.ContainerName}} {{if eq .Action "exec_die" }}process{{end}} exited non-zero: {{.ExitCode}}
Action: {{.EventMessage.Action}}
Image: {{.Image}}
ID: {{.ContainerID}}
`

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

func evalTemplate(t string, e ContainerEvent) string {
	buf := new(bytes.Buffer)
	tmpl, err := template.New("containerEventBody").Parse(t)
	if err != nil {
		log.Fatal(err)
	}
	err = tmpl.Execute(buf, e)
	if err != nil {
		log.Fatal(err)
	}
	return buf.String()
}

func dogstatsdEvent(c *statsd.Client, e ContainerEvent) {
	event := statsd.NewEvent(e.Title, e.Body)
	event.AggregationKey = e.ContainerID
	event.AlertType = statsd.Error
	event.SourceTypeName = "DOCKER"
	event.Tags = e.Tags
	err := c.Event(event)
	if err != nil {
		log.Fatal(err)
	} else {
		log.Println("sent event to dogstatsd")
	}
}

func stdoutEvent(e ContainerEvent) {
	eventBody := evalTemplate(stdoutEventTemplate, e)
	fmt.Println(eventBody)
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
				if msg.Actor.Attributes["exitCode"] != "0" {
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
							if os.Getenv("DEBUG") == "pretty" {
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
						}

						containerEvent := ContainerEvent{
							ContainerID:   msg.Actor.ID,
							ContainerName: msg.Actor.Attributes["name"],
							Image:         msg.Actor.Attributes["image"],
							Cmd:           strings.Join(inspectResponse.Config.Cmd, " "),
							ExitCode:      msg.Actor.Attributes["exitCode"],
							EventMessage:  msg,
						}

						for key, value := range inspectResponse.Config.Labels {
							containerEvent.Tags = append(containerEvent.Tags, fmt.Sprintf("%s=%s", key, value))
						}

						if msg.Action == "die" {
							containerEvent.Title = fmt.Sprintf("%s container exited non-zero: %s", fromContainer, exitCode)
						} else if msg.Action == "exec_die" {
							containerEvent.Title = fmt.Sprintf("%s container process exited non-zero: %s", fromContainer, exitCode)
						}

						if output == "datadog" {
							dogstatsdEvent(dogstatsdClient, containerEvent)
						} else {
							stdoutEvent(containerEvent)
						}
					}
				}
			}
		}
	}
}
