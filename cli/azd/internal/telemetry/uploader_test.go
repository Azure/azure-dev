package telemetry

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strconv"
	"testing"
	"time"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
)

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
	/* #nosec G404 - Use of weak random number generator - false positive in test */
	fileName := strconv.FormatUint(rand.Uint64(), 10)
	/* #nosec G404 - Use of weak random number generator - false positive in test */
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
	for _, item := range tq.itemQueue {
		if now.Sub(item.readyTime) >= 0 {
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

	if indexToRemove != -1 {
		tq.itemQueue = slices.Delete(tq.itemQueue, indexToRemove, indexToRemove+1)
	}
	return nil
}

type TransmitterStub struct {
	seen         [][]byte
	mockResponse *appinsightsexporter.TransmissionResult
	mockError    error
}

func NewTransmitterStub() *TransmitterStub {
	return &TransmitterStub{
		seen: [][]byte{},
		// By default return 200
		mockResponse: &appinsightsexporter.TransmissionResult{
			StatusCode: 200,
		},
		mockError: nil,
	}
}

func (tr *TransmitterStub) Transmit(
	payload []byte,
	items appinsightsexporter.TelemetryItems,
) (*appinsightsexporter.TransmissionResult, error) {
	tr.seen = append(tr.seen, payload)

	if tr.mockError != nil {
		return nil, tr.mockError
	}

	return tr.mockResponse, nil
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

func TestUpload_OnHttpCompleteFailure_DiscardItem(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)
	messages := addMessages(queue)

	transmitter.mockResponse.StatusCode = 400

	err := syncUpload(uploader)
	assert.NoError(t, err)
	assert.Equal(t, messages, transmitter.seen)
	assert.Empty(t, queue.itemQueue)
}

func TestUpload_OnNetworkError_Requeue(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)
	messages := addMessages(queue)

	transmitter.mockError = fmt.Errorf("network error")

	err := syncUpload(uploader)
	assert.NoError(t, err)
	assert.Equal(t, messages, transmitter.seen)
	assert.Len(t, queue.itemQueue, len(messages))
	assertRetryCountOnAllItems(t, queue, 1)
}

func TestUpload_OnHttpError_Requeue(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)
	messages := addMessages(queue)

	transmitter.mockResponse.StatusCode = 503

	err := syncUpload(uploader)
	assert.NoError(t, err)
	assert.Equal(t, messages, transmitter.seen)
	assert.Len(t, queue.itemQueue, len(messages))
	assertRetryCountOnAllItems(t, queue, 1)
}

func TestUpload_OnHttpErrorWithRetryAfter_Requeue(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)
	messages := addMessages(queue)

	retryAfter := clock.Now().Add(time.Duration(100) * time.Millisecond)
	transmitter.mockResponse.StatusCode = 503
	transmitter.mockResponse.RetryAfter = &retryAfter

	err := syncUpload(uploader)
	assert.NoError(t, err)
	assert.Equal(t, messages, transmitter.seen)
	assert.Len(t, queue.itemQueue, len(messages))
	assertRetryCountOnAllItems(t, queue, 1)
	assertRetryDelayOnAllItems(t, queue, retryAfter)
}

func TestUpload_OnPartialHttpError_RequeueIndividualItem(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)

	items := 6
	payload, _ := makeTelemetryPayload(items)
	_ = queue.Enqueue(payload)

	transmitter.mockResponse.StatusCode = 206
	transmitter.mockResponse.Response = &appinsightsexporter.BackendResponse{
		ItemsAccepted: 4,
		ItemsReceived: items,
		Errors: []*appinsightsexporter.ItemTransmissionResult{
			{Index: 1, StatusCode: 400, Message: "Bad 1"},
			{Index: 3, StatusCode: 408, Message: "OK Later"},
		},
	}

	err := syncUpload(uploader)
	assert.NoError(t, err)
	assert.Len(t, transmitter.seen, 1)
	assert.Equal(t, payload, transmitter.seen[0])
	assert.Len(t, queue.itemQueue, 1)
	assertRetryCountOnAllItems(t, queue, 1)

	// Examine the queue for the partial item requeued
	requeuedItem := queue.itemQueue[0]
	oneItem, _ := makeTelemetryPayload(1)
	assert.Equal(t, oneItem, requeuedItem.message, "Exactly one telemetry item should be requeued.")
}

func TestUpload_OnPersistentFailure_DiscardItem(t *testing.T) {
	clock := clock.NewMock()
	transmitter, queue, uploader := setupUploader(clock)
	messages := addMessages(queue)

	transmitter.mockResponse.StatusCode = 503
	defaultTransmitRetryDelay = time.Duration(1) * time.Second

	for i := 0; i < maxRetryCount; i++ {
		err := syncUpload(uploader)
		assert.NoError(t, err)
		assert.Len(t, transmitter.seen, len(messages)*(i+1))
		assert.Len(t, queue.itemQueue, len(messages))
		assertRetryCountOnAllItems(t, queue, i+1)

		clock.Add(defaultTransmitRetryDelay)
	}

	// Last retry attempt -- items should be discarded
	err := syncUpload(uploader)
	assert.NoError(t, err)
	assert.Len(t, queue.itemQueue, 0)
}

func setupUploader(clock clock.Clock) (*TransmitterStub, *InMemoryTelemetryQueue, *TelemetryUploader) {
	transmitter := NewTransmitterStub()
	queue := NewInMemoryTelemetryQueue(clock)
	uploader := NewUploader(queue, transmitter, clock, false)

	return transmitter, queue, uploader
}

func syncUpload(uploader *TelemetryUploader) error {
	result := make(chan error, 1)
	uploader.Upload(context.Background(), result)
	err := <-result
	close(result)
	return err
}

func addMessages(queue *InMemoryTelemetryQueue) [][]byte {
	messages := [][]byte{
		[]byte("1"),
		[]byte("2"),
		[]byte("3"),
	}

	for _, message := range messages {
		_ = queue.Enqueue(message)
	}

	return messages
}

func assertRetryCountOnAllItems(t *testing.T, queue *InMemoryTelemetryQueue, retryCount int) {
	for _, item := range queue.itemQueue {
		assert.Equal(t, retryCount, item.retryCount)
	}
}

func assertRetryDelayOnAllItems(t *testing.T, queue *InMemoryTelemetryQueue, retryAfter time.Time) {
	for _, item := range queue.itemQueue {
		assert.WithinDuration(t, retryAfter, item.readyTime, time.Duration(100)*time.Millisecond)
	}
}

func makeTelemetryPayload(count int) ([]byte, appinsightsexporter.TelemetryItems) {
	var buffer appinsightsexporter.TelemetryItems
	for i := 0; i < count; i++ {
		buffer = append(buffer, *appinsightsexporter.SpanToEnvelope(GetSpanStub().Snapshot()))
	}

	return buffer.Serialize(), buffer
}
