package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/bitly/go-nsq"
	"github.com/google/go-github/github"
)

const (
	// LabelsAttribute is the key in a GitHub payload for the labels.
	LabelsAttribute = "pull_request.labels"
)

func NewMessageHandler(client *github.Client, repo *Repository, pauseLock *sync.RWMutex) *MessageHandler {
	return &MessageHandler{
		client:    client,
		repo:      repo,
		store:     NewTransformingBlobStore(),
		pauseLock: pauseLock,
	}
}

type MessageHandler struct {
	client *github.Client
	repo   *Repository
	store  blobStore

	// The RWMutex allows us to implement pausing: all MessageHandler share the
	// same instance and take a read lock when they start handling a message.
	// The main loop takes the write lock when it needs to run a synchronous
	// operation, effectively pausing all queue processing.
	//
	// The nice properties of this solution is that:
	//  - Multiple MessageHandler can still process in parallel.
	//	- Processing still happens in HandleMessage, hence the returned error
	//    can be used by NSQ as an indicator to reemit.
	pauseLock *sync.RWMutex
}

func (m *MessageHandler) HandleMessage(n *nsq.Message) error {
	m.pauseLock.RLock()
	defer m.pauseLock.RUnlock()

	var p partialMessage
	if err := json.Unmarshal(n.Body, &p); err != nil {
		log.Error(err)
		return nil // No need to retry
	}
	return m.handleEvent(n.Timestamp, p.GitHubEvent, p.GitHubDelivery, n.Body)
}

func (m *MessageHandler) handleEvent(timestamp int64, event, delivery string, payload json.RawMessage) error {
	// Check if we are subscribed to this particular event type.
	if !m.repo.IsSubscribed(event) {
		log.Debugf("ignoring event %q for repository %s", event, m.repo.PrettyName())
		return nil
	}
	log.Infof("receive event %q for repository %q", event, m.repo.PrettyName())

	// Create the blob object and complete any data that needs to be.
	b, err := NewBlobFromPayload(event, delivery, payload)
	if err = m.prepareForStorage(b); err != nil {
		log.Errorf("preparing event %q for storage: %v", event, err)
		return err
	}

	// Take the timestamp from the NSQ Message (useful if the queue was put on
	// hold or if the process is catching up). This timestamp is a UnixNano.
	b.Timestamp = time.Unix(0, timestamp)
	return m.store.Index(StoreLiveEvent, m.repo, b)
}

func (m *MessageHandler) prepareForStorage(o *Blob) error {
	if o.Type != EvtPullRequest || o.HasAttribute(LabelsAttribute) {
		return nil
	}
	number := o.Data.Get("number").MustInt()
	log.Debugf("fetching labels for %s #%d", m.repo.PrettyName(), number)
	l, _, err := m.client.Issues.ListLabelsByIssue(m.repo.User, m.repo.Repo, number, &github.ListOptions{})
	if err != nil {
		return fmt.Errorf("retrieve labels for issue %d: %v", number, err)
	}

	// TODO This is terrible
	var b bytes.Buffer
	var d []interface{}
	if err := json.NewEncoder(&b).Encode(l); err != nil {
		return fmt.Errorf("serializing labels: %v", err)
	}
	if err := json.Unmarshal(b.Bytes(), &d); err != nil {
		return fmt.Errorf("unserializing labels: %v", err)
	}

	o.Push(LabelsAttribute, d)
	return nil
}
