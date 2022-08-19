package telemetry

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"testing"
	"time"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
	"golang.org/x/exp/slices"
)

type SQueue interface {
	Enqueue(message []byte) error
	EnqueueWithDelay(message []byte, delayDuration time.Duration, retryCount int) error
	Peek() (*StoredItem, error)
	Remove(item *StoredItem) error
}

type InMemoryItem struct {
	StoredItem

	readyTime time.Time
}

type InMemoryTelemetryQueue struct {
	itemQueue []InMemoryItem
	itemMap   map[string]struct{}
	clock     clock.Clock
}

func NewInMemoryTelemetryQueue(clock clock.Clock) *InMemoryTelemetryQueue {
	return &InMemoryTelemetryQueue{
		itemQueue: []InMemoryItem{},
		itemMap:   map[string]struct{}{},
		clock:     clock,
	}
}

func (tq *InMemoryTelemetryQueue) Enqueue(message []byte) error {
	return tq.save(message, time.Duration(0), 0)
}

func (tq *InMemoryTelemetryQueue) EnqueueWithDelay(message []byte, delayDuration time.Duration, retryCount int) error {
	return tq.save(message, delayDuration, retryCount)
}

func (tq *InMemoryTelemetryQueue) save(message []byte, delayDuration time.Duration, retryCount int) error {
	fileName := strconv.FormatUint(rand.Uint64(), 10)
	for _, exists := tq.itemMap[fileName]; exists; fileName = strconv.FormatUint(rand.Uint64(), 10) {
	}

	item := InMemoryItem{
		StoredItem: StoredItem{
			retryCount: retryCount,
			message:    message,
			fileName:   fileName,
		},
		readyTime: tq.clock.Now().Add(delayDuration),
	}

	tq.itemQueue = append(tq.itemQueue, item)
	tq.itemMap[fileName] = struct{}{}
	return nil
}

func (tq *InMemoryTelemetryQueue) Peek() (*StoredItem, error) {
	now := tq.clock.Now()
	for i := len(tq.itemQueue) - 1; i > 0; i-- {
		if now.Sub(tq.itemQueue[i].readyTime) >= 0 {
			item := tq.itemQueue[i]
			return &StoredItem{
				retryCount: item.retryCount,
				message:    item.message,
				fileName:   item.fileName,
			}, nil
		}
	}

	return nil, nil
}

func (tq *InMemoryTelemetryQueue) Remove(item *StoredItem) error {
	delete(tq.itemMap, item.fileName)

	indexToRemove := -1
	for i := range tq.itemQueue {
		if tq.itemQueue[i].fileName == item.fileName {
			indexToRemove = i
		}
	}

	tq.itemQueue = slices.Delete(tq.itemQueue, indexToRemove, indexToRemove)
	return nil
}

type TransmitterStub struct {
	seen           [][]byte
	mockResponse   *appinsightsexporter.TransmissionResult
	mockError      error
	mockStatusCode *int
}

func NewTransmitterStub() *TransmitterStub {
	return &TransmitterStub{
		seen:           [][]byte{},
		mockResponse:   nil,
		mockStatusCode: nil,
		mockError:      nil,
	}
}

func (tr *TransmitterStub) Transmit(payload []byte, items appinsightsexporter.TelemetryItems) (*appinsightsexporter.TransmissionResult, error) {
	tr.seen = append(tr.seen, payload)

	if tr.mockError != nil {
		return nil, tr.mockError
	}

	if tr.mockStatusCode != nil {
		return &appinsightsexporter.TransmissionResult{
			StatusCode: *tr.mockStatusCode,
		}, nil
	}

	if tr.mockResponse != nil {
		return tr.mockResponse, nil
	}

	// By default, return 200
	return &appinsightsexporter.TransmissionResult{
		StatusCode: 200,
	}, nil
}

func TestUploadEmpty(t *testing.T) {
	clock := clock.NewMock()
	_, _, uploader := setupUploader(clock)

	err := syncUpload(uploader)
	assert.NoError(t, err)
}

func TestUploadSuccess(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)
	messages := addMessages(queue)

	err := syncUpload(uploader)
	assert.NoError(t, err)
	assert.Equal(t, messages, transmitter.seen)
	assert.Empty(t, queue.itemQueue)
}

func TestUpload_HttpCompleteFailure(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)
	messages := addMessages(queue)

	errorStatusCode := 400
	transmitter.mockStatusCode = &errorStatusCode

	err := syncUpload(uploader)
	assert.Error(t, err)
	assert.Equal(t, messages, transmitter.seen)
	assert.Empty(t, queue.itemQueue)
}

func TestUpload_RequeueOnNetworkError(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)
	messages := addMessages(queue)

	transmitter.mockError = fmt.Errorf("network error")

	err := syncUpload(uploader)
	assert.Error(t, err)
	assert.Equal(t, messages, transmitter.seen)
	assert.Len(t, queue.itemQueue, len(messages))
	assertRetryCountOnAllItems(t, queue, 1)
}

func TestUpload_RequeueOnHttpError(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)
	messages := addMessages(queue)

	errorStatusCode := 503
	transmitter.mockStatusCode = &errorStatusCode

	err := syncUpload(uploader)
	assert.Error(t, err)
	assert.Equal(t, messages, transmitter.seen)
	assert.Len(t, queue.itemQueue, len(messages))
	assertRetryCountOnAllItems(t, queue, 1)
}

func TestUpload_RequeueOnHttpError_WithRetryAfter(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)
	messages := addMessages(queue)

	retryAfter := clock.Now().Add(time.Duration(3) * time.Second)
	errorStatusCode := 429
	transmitter.mockStatusCode = &errorStatusCode
	transmitter.mockResponse.RetryAfter = &retryAfter

	err := syncUpload(uploader)
	assert.Error(t, err)
	assert.Equal(t, messages, transmitter.seen)
	assert.Len(t, queue.itemQueue, len(messages))
	assertRetryCountOnAllItems(t, queue, 1)
	assertRetryDelayOnAllItems(t, queue, retryAfter)
}

func TestUpload_RequeueOnPartialHttpError(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)

	payload, _ := makeTelemetryPayload()
	messages := [][]byte{
		payload,
		payload,
		payload,
		payload,
	}

	errorStatusCode := 206
	transmitter.mockStatusCode = &errorStatusCode
	transmitter.mockResponse.Response = &appinsightsexporter.BackendResponse{
		ItemsAccepted: 2,
		ItemsReceived: 4,
		Errors: []*appinsightsexporter.ItemTransmissionResult{
			{Index: 1, StatusCode: 400, Message: "Bad 1"},
			{Index: 3, StatusCode: 408, Message: "OK Later"},
		},
	}

	err := syncUpload(uploader)
	assert.Error(t, err)
	assert.Equal(t, messages, transmitter.seen)
	assert.Len(t, queue.itemQueue, len(messages)-transmitter.mockResponse.Response.ItemsAccepted)
	assertRetryCountOnAllItems(t, queue, 1)
}

func setupUploader(clock clock.Clock) (*TransmitterStub, *InMemoryTelemetryQueue, *Uploader) {
	transmitter := NewTransmitterStub()
	queue := NewInMemoryTelemetryQueue(clock)
	uploader := NewUploader(queue, transmitter, true)

	return transmitter, queue, uploader
}

func syncUpload(uploader *Uploader) error {
	result := make(chan error)
	uploader.Upload(context.Background(), result)
	err := <-result
	return err
}

func addMessages(queue *InMemoryTelemetryQueue) [][]byte {
	messages := [][]byte{
		[]byte("hello, world"),
		[]byte("hello, world 2"),
		[]byte("hello, world 3"),
	}

	for _, message := range messages {
		queue.Enqueue(message)
	}

	return messages
}

func assertRetryCountOnAllItems(t *testing.T, queue *InMemoryTelemetryQueue, retryCount int) {
	for _, item := range queue.itemQueue {
		assert.Equal(t, item.retryCount, retryCount)
	}
}

func assertRetryDelayOnAllItems(t *testing.T, queue *InMemoryTelemetryQueue, retryAfter time.Time) {
	for _, item := range queue.itemQueue {
		assert.WithinDuration(t, retryAfter, item.readyTime, time.Duration(100)*time.Millisecond)
	}
}

func makeTelemetryPayload() ([]byte, appinsightsexporter.TelemetryItems) {
	var buffer appinsightsexporter.TelemetryItems
	for i := 0; i < 7; i++ {
		buffer = append(buffer, *appinsightsexporter.SpanToEnvelope(GetSpanStub().Snapshot()))
	}

	return buffer.Serialize(), buffer
}
