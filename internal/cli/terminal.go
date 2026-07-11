// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/ArdurAI/sith/internal/localops"
)

type terminalSession struct {
	input  *os.File
	output *os.File
	state  *term.State
	ticker *time.Ticker
	done   chan struct{}
	last   localops.TerminalSize
	first  bool
	once   sync.Once
}

func prepareTerminal(input io.Reader, output io.Writer) (*terminalSession, error) {
	inputFile, inputOK := input.(*os.File)
	outputFile, outputOK := output.(*os.File)
	if !inputOK || !outputOK || !term.IsTerminal(int(inputFile.Fd())) || !term.IsTerminal(int(outputFile.Fd())) {
		return nil, fmt.Errorf("--tty requires terminal stdin and stdout")
	}
	width, height, err := term.GetSize(int(outputFile.Fd()))
	if err != nil {
		return nil, fmt.Errorf("read terminal size: %w", err)
	}
	state, err := term.MakeRaw(int(inputFile.Fd()))
	if err != nil {
		return nil, fmt.Errorf("configure raw terminal: %w", err)
	}
	return &terminalSession{
		input: inputFile, output: outputFile, state: state, ticker: time.NewTicker(250 * time.Millisecond),
		done: make(chan struct{}),
		last: localops.TerminalSize{Width: terminalDimension(width), Height: terminalDimension(height)}, first: true,
	}, nil
}

func (terminal *terminalSession) Next() *localops.TerminalSize {
	if terminal.first {
		terminal.first = false
		size := terminal.last
		return &size
	}
	for {
		select {
		case <-terminal.done:
			return nil
		case <-terminal.ticker.C:
			width, height, err := term.GetSize(int(terminal.output.Fd()))
			if err != nil {
				continue
			}
			size := localops.TerminalSize{Width: terminalDimension(width), Height: terminalDimension(height)}
			if size == terminal.last {
				continue
			}
			terminal.last = size
			return &size
		}
	}
}

func terminalDimension(value int) uint16 {
	if value <= 0 {
		return 1
	}
	if value > int(^uint16(0)) {
		return ^uint16(0)
	}
	return uint16(value) // #nosec G115 -- value is bounded to the uint16 range above.
}

func (terminal *terminalSession) Close() {
	terminal.once.Do(func() {
		terminal.ticker.Stop()
		close(terminal.done)
		_ = term.Restore(int(terminal.input.Fd()), terminal.state)
	})
}
