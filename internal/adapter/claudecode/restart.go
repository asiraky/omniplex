package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Claude Code pins a conversation's system prompt in its transcript. On the
// first request it writes an attachment entry of type prompt_snapshot whose
// systemPrompt is the prompt it sent, and a compaction writes a fresh one; on
// resume it sends the prompt from the snapshot and ignores the one it was
// started with. So a restart that is meant to pick up a new system prompt has
// to put it in the snapshots first, while no process has the file open.
//
// This is an undocumented part of the transcript format (seen on Claude Code
// 2.1.327), which is why every surprise is an error: a restart that cannot
// apply the prompt must not resume quietly on the old one.
//
// systemPrompt is a list of blocks, the first of which is exactly the custom
// prompt omniplex passed; the rest are Claude Code's own and are kept. The
// snapshot's tools are left alone: a server added since is callable on resume
// without touching them.

// restartPrompt puts prompt into the pinned system prompt of conversation
// raw (an id as conversationID gives it) under configDir. A rewind not yet
// written is rebuilt from the conversation it was cut from, so that is the
// transcript rewritten; one that starts empty has no transcript and takes the
// new prompt from its start, so there is nothing to do.
func restartPrompt(configDir, cwd, raw, prompt string) error {
	id := parseConversation(raw).moveID(configDir, cwd)
	if id == "" {
		return nil
	}
	file, err := findConversation(configDir, cwd, id)
	if err != nil {
		return fmt.Errorf("restart: look for Claude Code's transcript for this conversation: %w", err)
	}
	if file == "" {
		return errors.New("restart: Claude Code has no transcript for this conversation, so the new system prompt can't be applied")
	}
	return rewritePromptSnapshots(file, prompt)
}

// rewritePromptSnapshots sets the system prompt of every prompt_snapshot
// entry in transcript file to prompt. Only those lines are re-encoded; every
// other line, valid JSON or not, is copied as it is. The file is replaced
// whole, through a temporary file beside it, so a failure part way leaves the
// old transcript in place and a resume never reads half of one.
func rewritePromptSnapshots(file, prompt string) error {
	// A transcript reached through a link is rewritten where it lives, not
	// replaced by a copy where the link was.
	path, err := filepath.EvalSymlinks(file)
	if err != nil {
		return fmt.Errorf("restart: open Claude Code's transcript: %w", err)
	}
	in, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("restart: open Claude Code's transcript: %w", err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("restart: open Claude Code's transcript: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".restart-*")
	if err != nil {
		return cantWrite(err)
	}
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return cantWrite(err)
	}

	// Lines run to megabytes (a tool result, a pasted image), so they are
	// read whole rather than through a Scanner's fixed limit.
	r := bufio.NewReaderSize(in, 1<<20)
	w := bufio.NewWriterSize(tmp, 1<<20)
	found := 0
	for {
		line, rerr := r.ReadBytes('\n')
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return fmt.Errorf("restart: read Claude Code's transcript: %w", rerr)
		}
		if len(line) > 0 {
			out, changed, err := rewriteSnapshotLine(line, prompt)
			if err != nil {
				return err
			}
			if changed {
				found++
			}
			if _, err := w.Write(out); err != nil {
				return cantWrite(err)
			}
		}
		if rerr != nil {
			break
		}
	}
	if found == 0 {
		return errors.New("restart: Claude Code's transcript for this conversation has no prompt snapshot, so the new system prompt can't be applied")
	}
	if err := w.Flush(); err != nil {
		return cantWrite(err)
	}
	if err := tmp.Sync(); err != nil {
		return cantWrite(err)
	}
	if err := tmp.Close(); err != nil {
		return cantWrite(err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		done = true
		return fmt.Errorf("restart: can't replace Claude Code's transcript, so the new system prompt can't be applied: %w", err)
	}
	done = true
	// The rename is only durable once the directory is; losing it to a crash
	// leaves the old transcript, which is no worse than not restarting.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// rewriteSnapshotLine returns line with its system prompt set to prompt when
// it is a prompt_snapshot entry, and line itself otherwise. The line keeps
// its ending. A snapshot whose system prompt is not the shape this knows is
// an error, not something to skip: the resume would run on the old prompt.
func rewriteSnapshotLine(line []byte, prompt string) ([]byte, bool, error) {
	// Most lines are not snapshots, and some are megabytes: only a line that
	// names one is worth decoding.
	if !bytes.Contains(line, []byte("prompt_snapshot")) {
		return line, false, nil
	}
	body := bytes.TrimRight(line, "\r\n")
	ending := line[len(body):]

	var entry map[string]json.RawMessage
	if json.Unmarshal(body, &entry) != nil {
		return line, false, nil
	}
	if str(entry["type"]) != "attachment" {
		return line, false, nil
	}
	var att map[string]json.RawMessage
	if json.Unmarshal(entry["attachment"], &att) != nil || str(att["type"]) != "prompt_snapshot" {
		return line, false, nil
	}

	sp, err := replacePrompt(att["systemPrompt"], prompt)
	if err != nil {
		return nil, false, err
	}
	att["systemPrompt"] = sp
	if entry["attachment"], err = encodeVerbatim(att); err != nil {
		return nil, false, err
	}
	out, err := encodeVerbatim(entry)
	if err != nil {
		return nil, false, err
	}
	return append(out, ending...), true, nil
}

// replacePrompt is a snapshot's systemPrompt with omniplex's own block, the
// first, replaced by prompt. Claude Code's blocks after it are kept.
func replacePrompt(raw json.RawMessage, prompt string) (json.RawMessage, error) {
	unrecognised := errors.New("restart: Claude Code's transcript has a prompt snapshot in a form omniplex doesn't recognise, so the new system prompt can't be applied")
	enc, err := encodeVerbatim(prompt)
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, unrecognised
	}
	switch raw[0] {
	case '"':
		return enc, nil
	case '[':
		var blocks []json.RawMessage
		if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 {
			return nil, unrecognised
		}
		if first := bytes.TrimSpace(blocks[0]); len(first) == 0 || first[0] != '"' {
			return nil, unrecognised
		}
		blocks[0] = enc
		return encodeVerbatim(blocks)
	}
	return nil, unrecognised
}

// encodeVerbatim encodes v without escaping <, > and &, which are all over a
// prompt, so the values carried through a rewritten line read as Claude Code
// wrote them.
func encodeVerbatim(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("restart: encode Claude Code's prompt snapshot: %w", err)
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func cantWrite(err error) error {
	return fmt.Errorf("restart: can't write Claude Code's transcript, so the new system prompt can't be applied: %w", err)
}
