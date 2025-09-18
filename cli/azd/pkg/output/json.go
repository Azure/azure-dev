// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/mattn/go-colorable"
)

type JsonFormatter struct {
}

func (f *JsonFormatter) Kind() Format {
	return JsonFormat
}

func (f *JsonFormatter) Format(obj interface{}, writer io.Writer, _ interface{}) error {
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}

	_, err = writer.Write(b)
	if err != nil {
		return err
	}

	_, err = writer.Write([]byte("\n"))
	if err != nil {
		return err
	}

	return nil
}

var _ Formatter = (*JsonFormatter)(nil)

// jsonObjectForMessage creates a json object representing a message. Any ANSI control sequences from the message are
// removed. A trailing newline is added to the message.
func EventForMessage(message string) contracts.EventEnvelope {
	// Strip any ANSI colors for the message.
	var buf bytes.Buffer

	// We do not expect the io.Copy to fail since none of these sub-calls will ever return an error (other than
	// EOF when we hit the end of the string)
	if _, err := io.Copy(colorable.NewNonColorable(&buf), strings.NewReader(message)); err != nil {
		panic(fmt.Sprintf("consoleMessageForMessage: did not expect error from io.Copy but got: %v", err))
	}

	// Add the newline that would have been added by fmt.Println when we wrote the message directly to the console.
	buf.WriteByte('\n')

	return newConsoleMessageEvent(buf.String())
}

func newConsoleMessageEvent(msg string) contracts.EventEnvelope {
	return contracts.EventEnvelope{
		Type:      contracts.ConsoleMessageEventDataType,
		Timestamp: time.Now(),
		Data: contracts.ConsoleMessage{
			Message: msg,
		},
	}
}
