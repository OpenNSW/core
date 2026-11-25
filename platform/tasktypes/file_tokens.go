package tasktypes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OpenNSW/core/shared/deepcopy"
	"github.com/OpenNSW/core/taskflow/store"

	"github.com/OpenNSW/nsw-srilanka/internal/tasks/filefields"
)

// FileTokens replaces the stored files in what a step sends another service
// with file tokens issued to that service's client, which it redeems on this
// deployment's storage route. A stored value never leaves as it is.
//
// Only the deployment that owns the files can issue them. A deployment whose
// storage proxies to the owner's stores the owner's tokens and sends them back
// as they are: it uses the zero FileTokens.
type FileTokens struct {
	// ClientFor returns the machine client the outbound service serviceID
	// calls back as.
	ClientFor func(serviceID string) (clientID string, ok bool)
	// IssueForClient returns a token that lets the machine client clientID
	// open value, and nothing more. It never expires: the service keeps it in its
	// own records.
	IssueForClient func(clientID, value string) (string, error)
	// RenderConfigOf returns a task's render config, whose files list names
	// the file fields to tokenize. Required with IssueForClient.
	RenderConfigOf func(ctx context.Context, record store.TaskRecord) (json.RawMessage, error)
}

// tokenize returns a copy of data, the value at the top-level field root of a
// task's data ("" for all of it), in which each file field record's render
// config declares holds a token for serviceID's client. data itself is not
// changed. A file to send with no client to issue it to is an error, not sent
// as it is.
func (f FileTokens) tokenize(ctx context.Context, record store.TaskRecord, root string, data any, serviceID string) (any, error) {
	if f.IssueForClient == nil {
		return data, nil
	}
	renderConfig, err := f.RenderConfigOf(ctx, record)
	if err != nil {
		return nil, err
	}
	paths, err := filefields.Parse(renderConfig)
	if err != nil {
		return nil, configError(err)
	}
	var inData []filefields.Path
	for _, path := range paths {
		if rebased, ok := path.Rebase(root); ok {
			inData = append(inData, rebased)
		}
	}
	if len(inData) == 0 {
		return data, nil
	}
	sent := deepcopy.Value(data)
	err = filefields.Replace(sent, inData, func(value string) (string, error) {
		clientID, ok := f.ClientFor(serviceID)
		if !ok {
			return "", configError(fmt.Errorf("service %q maps to no client in the catalog, so it cannot be sent file tokens", serviceID))
		}
		return f.IssueForClient(clientID, value)
	})
	if err != nil {
		return nil, err
	}
	return sent, nil
}
