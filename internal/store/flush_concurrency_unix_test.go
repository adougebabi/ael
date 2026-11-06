//go:build darwin || linux

package store

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestFlushWriterSubprocessHelper(t *testing.T) {
	if os.Getenv("AEL_TEST_FLUSH_CHILD") != "1" {
		return
	}
	s, err := Open(os.Getenv("AEL_TEST_FLUSH_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fmt.Println("child-opened")
	if _, err = bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	fmt.Println("child-writing")
	if err = s.Write(func(tx *Tx) error { return tx.File(os.Getenv("AEL_TEST_FLUSH_PATH"), []byte("C")) }); err != nil {
		t.Fatal(err)
	}
}

func TestFlushSerializesWriterInAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	gate, path := filepath.Join(dir, "gate"), filepath.Join(dir, "record")
	if err = syscall.Mkfifo(gate, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO outbox(path,old_hash,body,state) VALUES(?,?,?,'pending')", gate, Hash([]byte("gate")), []byte("gate")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("A"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Write(func(tx *Tx) error { return tx.File(path, []byte("B")) }); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestFlushWriterSubprocessHelper$")
	child.Env = append(os.Environ(), "AEL_TEST_FLUSH_CHILD=1", "AEL_TEST_FLUSH_DIR="+dir, "AEL_TEST_FLUSH_PATH="+path)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	readLine := func(want string) {
		t.Helper()
		select {
		case line := <-lines:
			if line != want {
				t.Fatalf("child output %q, want %q", line, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("child did not report %s", want)
		}
	}
	readLine("child-opened")
	ready, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseGate := func() { once.Do(func() { close(release) }) }
	defer releaseGate()
	gateDone := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(gate, os.O_WRONLY, 0600)
		if err != nil {
			gateDone <- err
			return
		}
		close(ready)
		<-release
		_, err = f.Write([]byte("gate"))
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		gateDone <- err
	}()
	flushDone := make(chan error, 1)
	go func() { flushDone <- s.Flush() }()
	select {
	case <-ready:
	case err := <-flushDone:
		t.Fatal("flush ended before gate", err)
	case <-time.After(5 * time.Second):
		t.Fatal("flush did not reach gate")
	}
	if _, err = fmt.Fprintln(stdin, "go"); err != nil {
		t.Fatal(err)
	}
	readLine("child-writing")
	childDone := make(chan error, 1)
	go func() { childDone <- child.Wait() }()
	select {
	case err := <-childDone:
		t.Fatal("child passed a flush in progress", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseGate()
	if err = <-gateDone; err != nil {
		t.Fatal(err)
	}
	if err = <-flushDone; err != nil {
		t.Fatal(err)
	}
	if err = <-childDone; err != nil {
		t.Fatal("child write failed", err)
	}
	if err = s.Flush(); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "C" {
		t.Fatal("subprocess update lost", string(content), err)
	}
}

func TestFlushSerializesConcurrentWriterAcrossStoreHandles(t *testing.T) {
	for _, separate := range []bool{false, true} {
		name := "same store"
		if separate {
			name = "separate stores"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			other := s
			if separate {
				other, err = Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
			}
			gate, path := filepath.Join(dir, "gate"), filepath.Join(dir, "record")
			if err = syscall.Mkfifo(gate, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec("INSERT INTO outbox(path,old_hash,body,state) VALUES(?,?,?,'pending')", gate, Hash([]byte("gate")), []byte("gate")); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, []byte("A"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = s.Write(func(tx *Tx) error { return tx.File(path, []byte("B")) }); err != nil {
				t.Fatal(err)
			}
			ready, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			releaseGate := func() { once.Do(func() { close(release) }) }
			defer releaseGate()
			gateDone := make(chan error, 1)
			go func() {
				file, err := os.OpenFile(gate, os.O_WRONLY, 0600)
				if err != nil {
					gateDone <- err
					return
				}
				close(ready)
				<-release
				_, err = file.Write([]byte("gate"))
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
				gateDone <- err
			}()
			flushDone := make(chan error, 1)
			go func() { flushDone <- s.Flush() }()
			select {
			case <-ready:
			case err := <-flushDone:
				t.Fatal("flush ended before reading gate", err)
			case <-time.After(5 * time.Second):
				t.Fatal("flush did not reach gate")
			}
			writeStarted, writeDone := make(chan struct{}), make(chan error, 1)
			go func() {
				close(writeStarted)
				writeDone <- other.Write(func(tx *Tx) error { return tx.File(path, []byte("C")) })
			}()
			<-writeStarted
			select {
			case err := <-writeDone:
				t.Fatal("writer passed a flush in progress", err)
			case <-time.After(100 * time.Millisecond):
			}
			releaseGate()
			if err = <-gateDone; err != nil {
				t.Fatal(err)
			}
			if err = <-flushDone; err != nil {
				t.Fatal(err)
			}
			if err = <-writeDone; err != nil {
				t.Fatal(err)
			}
			if err = other.Flush(); err != nil {
				t.Fatal("false conflict after serialized update", err)
			}
			result, err := os.ReadFile(path)
			if err != nil || string(result) != "C" {
				t.Fatal("latest intent was not preserved", string(result), err)
			}
		})
	}
}

func TestFlushRetainsUserConflictAndRecoversEarlierAppliedFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	if err = os.WriteFile(first, []byte("old-first"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(second, []byte("old-second"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Write(func(tx *Tx) error {
		if err := tx.File(first, []byte("new-first")); err != nil {
			return err
		}
		return tx.File(second, []byte("new-second"))
	}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(second, []byte("user-edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(); err == nil || !strings.Contains(err.Error(), "FILE_CONFLICT") {
		t.Fatal("user edit should remain a conflict", err)
	}
	if content, _ := os.ReadFile(second); string(content) != "user-edit" {
		t.Fatal("user edit was overwritten", string(content))
	}
	if err = s.Write(func(tx *Tx) error { return tx.File(first, []byte("newer-first")) }); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(second, []byte("old-second"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(); err != nil {
		t.Fatal("retry should recognize the already applied first intent", err)
	}
	if content, _ := os.ReadFile(first); string(content) != "newer-first" {
		t.Fatal("first intent was lost", string(content))
	}
	if content, _ := os.ReadFile(second); string(content) != "new-second" {
		t.Fatal("second intent was lost", string(content))
	}
}

func TestAppliedIntentAfterCrashCanBeSuperseded(t *testing.T) {
	for _, scenario := range []string{"write already applied", "delete already applied", "delete after applied write", "external edit"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			path := filepath.Join(dir, "record")
			state, disk := "pending", "B"
			if scenario == "delete already applied" {
				state, disk = "delete_pending", ""
			} else if scenario == "external edit" {
				disk = "user-edit"
			}
			if disk != "" {
				if err = os.WriteFile(path, []byte(disk), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.DB.Exec("INSERT INTO outbox(path,old_hash,body,state) VALUES(?,?,?,?)", path, Hash([]byte("A")), []byte("B"), state); err != nil {
				t.Fatal(err)
			}
			err = s.Write(func(tx *Tx) error {
				if scenario == "delete after applied write" {
					return tx.DeleteFile(path)
				}
				return tx.File(path, []byte("C"))
			})
			if err != nil {
				t.Fatal(err)
			}
			err = s.Flush()
			if scenario == "external edit" {
				if err == nil || !strings.Contains(err.Error(), "FILE_CONFLICT") {
					t.Fatal("external edit should remain protected", err)
				}
				if content, _ := os.ReadFile(path); string(content) != "user-edit" {
					t.Fatal("external edit was overwritten", string(content))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			content, readErr := os.ReadFile(path)
			if scenario == "delete after applied write" {
				if !os.IsNotExist(readErr) {
					t.Fatal("delete was not applied", readErr)
				}
			} else if readErr != nil || string(content) != "C" {
				t.Fatal("new intent was not applied", string(content), readErr)
			}
		})
	}
}

func TestDeleteCancelsUnflushedCreate(t *testing.T) {
	for _, sameTransaction := range []bool{true, false} {
		name := "separate transactions"
		if sameTransaction {
			name = "same transaction"
		}
		t.Run(name, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			path := filepath.Join(s.Dir, "new-file")
			if sameTransaction {
				err = s.Write(func(tx *Tx) error {
					if err := tx.File(path, []byte("not yet written")); err != nil {
						return err
					}
					return tx.DeleteFile(path)
				})
			} else {
				err = s.Write(func(tx *Tx) error { return tx.File(path, []byte("not yet written")) })
				if err == nil {
					err = s.Write(func(tx *Tx) error { return tx.DeleteFile(path) })
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Flush(); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("deleted pending file was created", err)
			}
		})
	}
}
