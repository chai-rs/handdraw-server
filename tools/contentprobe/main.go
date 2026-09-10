// Command contentprobe exchanges JSON with the cross-language contract harness; it opens no network connection.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/chai-rs/handdraw-server/internal/collaboration/infra/protocol"
	documentcodec "github.com/chai-rs/handdraw-server/internal/document/infra/ygo"
	"github.com/chai-rs/handdraw-server/internal/document/model"
	"github.com/chai-rs/handdraw-server/internal/document/service"
)

type request struct {
	Action     string   `json:"action"`
	BoardID    string   `json:"board_id"`
	State      []byte   `json:"state"`
	Update     []byte   `json:"update"`
	Assets     []string `json:"assets"`
	Frame      []byte   `json:"frame"`
	FromClient bool     `json:"from_client"`
}
type response struct {
	State    []byte          `json:"state,omitempty"`
	Snapshot *model.Snapshot `json:"snapshot,omitempty"`
	Error    string          `json:"error,omitempty"`
	PageID   string          `json:"page_id,omitempty"`
	Frame    []byte          `json:"frame,omitempty"`
}

func main() {
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 32<<20))
	decoder.DisallowUnknownFields()

	var input request
	if err := decoder.Decode(&input); err != nil {
		fmt.Fprintln(os.Stderr, "invalid contract input")
		os.Exit(1)
	}

	result := run(input)
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}

func run(input request) response {
	codec := documentcodec.Codec{}

	scope := model.Validation{BoardID: input.BoardID, AllowedAssets: map[string]bool{}}
	for _, id := range input.Assets {
		scope.AllowedAssets[id] = true
	}

	var err error

	result := response{}

	switch input.Action {
	case "empty", "get_started":
		initial, e := service.NewInitialBuilder(codec).Build(input.BoardID, input.Action)
		err = e
		result.State = initial.State
		result.PageID = initial.PageID
	case "apply":
		result.State, err = codec.Apply(input.State, input.Update, scope)
	case "roundtrip":
		var snapshot model.Snapshot

		snapshot, err = codec.Decode(input.State, scope)
		if err == nil {
			result.Snapshot = &snapshot
			result.State, err = codec.Encode(snapshot, scope)
		}
	case "control":
		control, e := protocol.Decode("board:"+input.BoardID, input.Frame, input.FromClient)

		err = e
		if err == nil {
			result.Frame, err = protocol.Encode("board:"+input.BoardID, control, input.FromClient)
		}
	default:
		err = model.ErrInvalidDocument
	}

	if err != nil {
		return response{Error: err.Error()}
	}

	return result
}
