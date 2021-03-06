package client

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/evergreen-ci/evergreen/apimodels"
	"github.com/evergreen-ci/evergreen/util"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/logging"
	"github.com/mongodb/grip/send"
)

const (
	defaultMaxAttempts  = 10
	defaultTimeoutStart = time.Second * 2
	defaultTimeoutMax   = time.Minute * 10
	heartbeatTimeout    = time.Minute * 1

	v1 = "/api/2"
)

// communicatorImpl implements Communicator and makes requests to API endpoints for the agent.
type communicatorImpl struct {
	serverURL    string
	maxAttempts  int
	timeoutStart time.Duration
	timeoutMax   time.Duration
	httpClient   *http.Client

	// these fields have setters
	hostID     string
	hostSecret string
	apiUser    string
	apiKey     string

	lastMessageSent time.Time
	mutex           sync.RWMutex
}

// TaskData contains the taskData.ID and taskData.Secret. It must be set for some client methods.
type TaskData struct {
	ID                 string
	Secret             string
	OverrideValidation bool
}

// NewCommunicator returns a Communicator capable of making HTTP REST requests against
// the API server. To change the default retry behavior, use the SetTimeoutStart, SetTimeoutMax,
// and SetMaxAttempts methods.
func NewCommunicator(serverURL string) Communicator {
	c := &communicatorImpl{
		maxAttempts:  defaultMaxAttempts,
		timeoutStart: defaultTimeoutStart,
		timeoutMax:   defaultTimeoutMax,
		serverURL:    serverURL,
		httpClient:   util.GetHttpClient(),
	}
	c.httpClient.Timeout = heartbeatTimeout
	return c
}

func (c *communicatorImpl) Close() { util.PutHttpClient(c.httpClient) }

// SetTimeoutStart sets the initial timeout for a request.
func (c *communicatorImpl) SetTimeoutStart(timeoutStart time.Duration) {
	c.timeoutStart = timeoutStart
}

// SetTimeoutMax sets the maximum timeout for a request.
func (c *communicatorImpl) SetTimeoutMax(timeoutMax time.Duration) {
	c.timeoutMax = timeoutMax
}

// SetMaxAttempts sets the number of attempts a request will be made.
func (c *communicatorImpl) SetMaxAttempts(attempts int) {
	c.maxAttempts = attempts
}

// SetHostID sets the host ID.
func (c *communicatorImpl) SetHostID(hostID string) {
	c.hostID = hostID
}

// SetHostSecret sets the host secret.
func (c *communicatorImpl) SetHostSecret(hostSecret string) {
	c.hostSecret = hostSecret
}

// GetHostID returns the host ID.
func (c *communicatorImpl) GetHostID() string {
	return c.hostID
}

// GetHostSecret returns the host secret.
func (c *communicatorImpl) GetHostSecret() string {
	return c.hostSecret
}

// SetAPIUser sets the API user.
func (c *communicatorImpl) SetAPIUser(apiUser string) {
	c.apiUser = apiUser
}

// SetAPIKey sets the API key.
func (c *communicatorImpl) SetAPIKey(apiKey string) {
	c.apiKey = apiKey
}

func (c *communicatorImpl) UpdateLastMessageTime() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.lastMessageSent = time.Now()
}

func (c *communicatorImpl) LastMessageAt() time.Time {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.lastMessageSent
}

// GetLogProducer
func (c *communicatorImpl) GetLoggerProducer(ctx context.Context, taskData TaskData) LoggerProducer {
	local := grip.GetSender()

	exec := newLogSender(ctx, c, apimodels.AgentLogPrefix, taskData)
	grip.CatchWarning(exec.SetFormatter(send.MakeDefaultFormatter()))
	exec = send.NewConfiguredMultiSender(local, exec)

	task := newTimeoutLogSender(ctx, c, apimodels.TaskLogPrefix, taskData)
	grip.CatchWarning(task.SetFormatter(send.MakeDefaultFormatter()))
	task = send.NewConfiguredMultiSender(local, task)

	system := newLogSender(ctx, c, apimodels.SystemLogPrefix, taskData)
	grip.CatchWarning(system.SetFormatter(send.MakeDefaultFormatter()))
	system = send.NewConfiguredMultiSender(local, system)

	return &logHarness{
		execution: logging.MakeGrip(exec),
		task:      logging.MakeGrip(task),
		system:    logging.MakeGrip(system),
	}
}
