package marathon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/kbolino/mesosdef/marathon/model"
)

const (
	mediaTypeJSON        = "application/json"
	mediaTypeEventStream = "text/event-stream"
)

var (
	eventKeyValueSeparator = []byte{':'}
	urlDeployAppPath       = mustParseURL("/v2/apps")
	urlStreamEventsPath    = mustParseURL("/v2/events")
)

type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
}

func (c *Client) DeployApp(request *model.DeployAppRequest) (*model.DeployAppResponse, error) {
	appID := request.ID
	if len(appID) == 0 {
		return nil, fmt.Errorf("app config does not contain id")
	}
	var requestBody bytes.Buffer
	if err := json.NewEncoder(&requestBody).Encode(request); err != nil {
		return nil, fmt.Errorf("failed to encode JSON form of request: %w", err)
	}
	requestURL := c.baseURL.ResolveReference(urlDeployAppPath).String()
	httpResponse, err := c.httpClient.Post(requestURL, mediaTypeJSON, &requestBody)
	if err != nil {
		return nil, fmt.Errorf("error deploying app %s with POST %s: %w", appID, requestURL, err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode > 299 {
		return nil, fmt.Errorf("unexpected response %d %s deploying app %s to POST %s: %w", httpResponse.StatusCode,
			httpResponse.Status, appID, requestURL, err)
	}
	var response model.DeployAppResponse
	if err := json.NewDecoder(httpResponse.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode JSON form of response to POST %s: %w", requestURL, err)
	}
	return &response, nil
}

type Event struct {
	ID   string
	Type string
	Data []byte
}

// StreamEvents requests a stream of events from Marathon, sends them to the
// given events channel, and runs until the server closes the connection,
// an error occurs, or stopSignal is closed.
// This method creates a goroutine to perform the actual HTTP request and
// parsing of the response body and blocks until that goroutine finishes,
// unless a panic occurs in which case the goroutine might leak.
func (c *Client) StreamEvents(events chan<- Event, stopSignal <-chan struct{}) error {
	if events == nil {
		return fmt.Errorf("events is nil")
	}
	requestURL := c.baseURL.ResolveReference(urlStreamEventsPath).String()
	httpRequest, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request for GET %s: %w", requestURL, err)
	}
	httpRequest.Header.Set("Accept", mediaTypeEventStream)
	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("error streaming events from GET %s: %w", requestURL, err)
	}
	var closeOnce sync.Once
	defer closeOnce.Do(func() { httpResponse.Body.Close() })
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode > 299 {
		return fmt.Errorf("unexpected response %d %s streaming events from GET %s: %w", httpResponse.StatusCode,
			httpResponse.Status, requestURL, err)
	}
	errors := make(chan error, 1)
	go func() {
		defer close(errors)
		scanner := bufio.NewScanner(httpResponse.Body)
		var event Event
		var dataBuf bytes.Buffer
	scanLoop:
		for scanner.Scan() {
			if stopSignal != nil {
				select {
				case _, ok := <-stopSignal:
					if !ok {
						break scanLoop
					}
				default:
				}
			}
			line := scanner.Bytes()
			parts := bytes.SplitN(line, eventKeyValueSeparator, 2)
			key := string(bytes.TrimSpace(parts[0]))
			if len(key) == 0 && len(parts) == 1 {
				// blank line, send event and start new one
				if len(event.ID) == 0 && len(event.Type) == 0 && dataBuf.Len() == 0 {
					continue
				}
				if dataBuf.Len() != 0 {
					event.Data = dataBuf.Bytes()
					dataBuf = bytes.Buffer{}
				}
				events <- event
				event = Event{}
			} else if len(key) == 0 && len(parts) == 2 {
				errors <- fmt.Errorf("received malformed value-only line [%s]", line)
				return
			} else {
				var value []byte
				if len(parts) == 2 {
					value = bytes.TrimSpace(parts[1])
				}
				switch key {
				case "id":
					if len(event.ID) != 0 {
						errors <- fmt.Errorf("received duplicate id for event, '%s' vs '%s'", event.ID, value)
						return
					}
					event.ID = string(value)
				case "event":
					if len(event.Type) != 0 {
						errors <- fmt.Errorf("received duplicate type for event, '%s' vs '%s'", event.Type, value)
						return
					}
					event.Type = string(value)
				case "data":
					dataBuf.Write(value)
				default:
					errors <- fmt.Errorf("received unsupported server-sent event message field '%s'", key)
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			errors <- fmt.Errorf("failed to scan event stream response to GET %s: %w", requestURL, err)
		}
	}()
	stopped := false
	var resultErr error
monitorLoop:
	for {
		select {
		case err, ok := <-errors:
			if err != nil {
				resultErr = err
			}
			if !ok {
				break monitorLoop
			}
		case _, ok := <-stopSignal:
			if !ok {
				stopped = true
				closeOnce.Do(func() { httpResponse.Body.Close() })
			}
		}
	}
	if stopped {
		// assume all errors are due to closing the body prematurely
		return nil
	}
	return resultErr
}

func mustParseURL(rawURL string) *url.URL {
	result, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return result
}
