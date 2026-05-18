package main

// `muveectl projects cp` — copy files between local and the project container
// in either direction, like kubectl cp. Wire protocol is open_cp + tar frames
// proxied through the agent control channel; `docker cp` on the agent does
// the actual tar packing/unpacking on the container side.

import (
	"archive/tar"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/hoveychen/muvee/internal/agentcontrol"
	"github.com/spf13/cobra"
)

var projectsCpCmd = &cobra.Command{
	Use:   "cp SRC DST",
	Short: "Copy files/dirs between local and the project container (like kubectl cp)",
	Long: `Copies in either direction. Exactly one of SRC or DST must reference
a project container as PROJECT:PATH; the other is a local path.

Examples:
  muveectl projects cp ./config.json my-project:/app/config.json
  muveectl projects cp my-project:/app/logs ./logs-dump`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		src, dst := args[0], args[1]
		srcRef, srcPath, srcRemote := parseCpRef(src)
		dstRef, dstPath, dstRemote := parseCpRef(dst)
		if srcRemote == dstRemote {
			return fmt.Errorf("exactly one of SRC or DST must be a container path (PROJECT:PATH)")
		}
		if srcRemote {
			return runProjectCpDownload(srcRef, srcPath, dstPath)
		}
		return runProjectCpUpload(dstRef, dstPath, srcPath)
	},
}

// parseCpRef classifies a cp argument as either a container reference
// (PROJECT:PATH) or a local file path. On Windows, a leading drive letter is
// preserved as a local path (e.g. C:\foo).
func parseCpRef(s string) (ref, path string, remote bool) {
	i := strings.Index(s, ":")
	if i <= 0 {
		return "", s, false
	}
	// Windows drive letters (single ASCII letter before ":") are local paths.
	if runtime.GOOS == "windows" && i == 1 {
		c := s[0]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return "", s, false
		}
	}
	return s[:i], s[i+1:], true
}

func openCpWebSocket(projectRef string) (*websocket.Conn, error) {
	if err := requireAuth(); err != nil {
		return nil, err
	}
	projectID, err := resolveProjectRef(cl, projectRef)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(cl.server)
	if err != nil {
		return nil, fmt.Errorf("parse server: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/projects/" + projectID + "/cp"
	header := http.Header{}
	header.Set("Authorization", "Bearer "+cl.token)
	ws, resp, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("dial: %s (%d)", err, resp.StatusCode)
		}
		return nil, fmt.Errorf("dial: %w", err)
	}
	return ws, nil
}

// runProjectCpUpload tars localPath and streams it to the container at remotePath.
func runProjectCpUpload(projectRef, remotePath, localPath string) error {
	if _, err := os.Stat(localPath); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	ws, err := openCpWebSocket(projectRef)
	if err != nil {
		return err
	}
	defer ws.Close()

	if err := agentcontrol.WriteFrame(ws, agentcontrol.Frame{
		Type:      agentcontrol.TypeOpenCp,
		Path:      remotePath,
		Direction: agentcontrol.CpDirectionUp,
	}); err != nil {
		return fmt.Errorf("send open_cp: %w", err)
	}

	// Tar localPath into a goroutine-fed pipe and chunk it into cp_up_tar frames.
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := tarFromDisk(tw, localPath)
		if cerr := tw.Close(); err == nil {
			err = cerr
		}
		pw.CloseWithError(err)
	}()

	buf := make([]byte, 32*1024)
	for {
		n, err := pr.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if werr := agentcontrol.WriteFrame(ws, agentcontrol.Frame{
				Type: agentcontrol.TypeCpUpTar,
				Data: chunk,
			}); werr != nil {
				return fmt.Errorf("send tar chunk: %w", werr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
	}
	if err := agentcontrol.WriteFrame(ws, agentcontrol.Frame{Type: agentcontrol.TypeCpEnd}); err != nil {
		return fmt.Errorf("send cp_end: %w", err)
	}

	return drainCpResult(ws)
}

// runProjectCpDownload pulls a tar stream from the container and writes it to localPath.
// If localPath is a directory, files are extracted into it; otherwise the
// single archived file is written to localPath.
func runProjectCpDownload(projectRef, remotePath, localPath string) error {
	ws, err := openCpWebSocket(projectRef)
	if err != nil {
		return err
	}
	defer ws.Close()

	if err := agentcontrol.WriteFrame(ws, agentcontrol.Frame{
		Type:      agentcontrol.TypeOpenCp,
		Path:      remotePath,
		Direction: agentcontrol.CpDirectionDown,
	}); err != nil {
		return fmt.Errorf("send open_cp: %w", err)
	}

	// Receive cp_down_tar frames into a pipe; untar on the other side.
	pr, pw := io.Pipe()
	untarErr := make(chan error, 1)
	go func() {
		untarErr <- untarToDisk(pr, localPath)
	}()

	var exitCode int
	for {
		f, err := agentcontrol.ReadFrame(ws)
		if err != nil {
			pw.CloseWithError(err)
			<-untarErr
			return fmt.Errorf("read frame: %w", err)
		}
		switch f.Type {
		case agentcontrol.TypeCpDownTar:
			if _, err := pw.Write(f.Data); err != nil {
				pw.CloseWithError(err)
				<-untarErr
				return fmt.Errorf("pipe write: %w", err)
			}
		case agentcontrol.TypeStdio:
			if f.Stream == agentcontrol.StreamStderr {
				os.Stderr.Write(f.Data)
			}
		case agentcontrol.TypeCpEnd:
			pw.Close()
		case agentcontrol.TypeExit:
			exitCode = f.Code
			if uterr := <-untarErr; uterr != nil {
				return fmt.Errorf("untar: %w", uterr)
			}
			if exitCode != 0 {
				return fmt.Errorf("docker cp exited %d", exitCode)
			}
			return nil
		case agentcontrol.TypeError:
			pw.CloseWithError(fmt.Errorf("server: %s", f.Msg))
			<-untarErr
			return fmt.Errorf("server error: %s", f.Msg)
		}
	}
}

// drainCpResult is used by upload after cp_end has been sent: it waits for
// the agent to send stderr (on failure) + exit, surfaces the result.
func drainCpResult(ws *websocket.Conn) error {
	for {
		f, err := agentcontrol.ReadFrame(ws)
		if err != nil {
			return fmt.Errorf("read frame: %w", err)
		}
		switch f.Type {
		case agentcontrol.TypeStdio:
			if f.Stream == agentcontrol.StreamStderr {
				os.Stderr.Write(f.Data)
			}
		case agentcontrol.TypeExit:
			if f.Code != 0 {
				return fmt.Errorf("docker cp exited %d", f.Code)
			}
			return nil
		case agentcontrol.TypeError:
			return fmt.Errorf("server error: %s", f.Msg)
		}
	}
}

// tarFromDisk tars the file or directory rooted at localPath. Entry names are
// relative to filepath.Base(localPath) so that `tar -x` (which is what
// docker-cp does on the container side) recreates the same basename inside
// the destination directory.
func tarFromDisk(tw *tar.Writer, localPath string) error {
	abs, err := filepath.Abs(localPath)
	if err != nil {
		return err
	}
	base := filepath.Base(abs)
	parent := filepath.Dir(abs)
	return filepath.Walk(abs, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			link, _ = os.Readlink(p)
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(parent, p)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, f)
			f.Close()
			if copyErr != nil {
				return copyErr
			}
		}
		_ = base
		return nil
	})
}

// untarToDisk unpacks an incoming tar stream into localPath. If localPath
// names an existing directory, archive entries are extracted into it
// preserving their relative names. Otherwise the (expected single-file)
// archive entry is written to localPath directly.
func untarToDisk(r io.Reader, localPath string) error {
	tr := tar.NewReader(r)
	info, statErr := os.Stat(localPath)
	dstIsDir := statErr == nil && info.IsDir()

	firstFileDone := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		var target string
		if dstIsDir {
			target = filepath.Join(localPath, filepath.FromSlash(hdr.Name))
		} else {
			if firstFileDone {
				return fmt.Errorf("multiple files in archive but destination %q is not a directory", localPath)
			}
			target = localPath
			firstFileDone = true
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		default:
			// Skip other entry kinds (hardlinks, device files, etc.) — out
			// of scope for the cp command.
		}
	}
}
