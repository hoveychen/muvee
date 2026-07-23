package deployer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// parseUserGID parses a docker image's Config.User string into a numeric
// uid/gid. It reports resolved=true only when the value is empty or fully
// numeric; a username (or non-numeric group) needs a lookup inside the image,
// so it returns resolved=false and the caller falls back to running the image.
//
//	""          -> 0, 0, true   (root default)
//	"1000"      -> 1000, 1000, true
//	"1000:2000" -> 1000, 2000, true
//	"relay"     -> 0, 0, false  (needs image lookup)
//	"0:staff"   -> 0, 0, false  (needs image lookup)
//
// When only a uid is given the gid defaults to the uid: the dir ends up owned
// by the image's user, whose owner-write bit is all that's needed to let the
// container populate its workspace.
func parseUserGID(user string) (uid, gid int, resolved bool) {
	user = strings.TrimSpace(user)
	if user == "" {
		return 0, 0, true
	}
	name, group, hasGroup := strings.Cut(user, ":")
	u, err := strconv.Atoi(name)
	if err != nil {
		return 0, 0, false
	}
	g := u
	if hasGroup {
		g, err = strconv.Atoi(group)
		if err != nil {
			return 0, 0, false
		}
	}
	return u, g, true
}

// imageRunUserGID resolves the uid/gid the given image runs its process as. It
// reads the image's configured user via `docker inspect`; if that user is a
// name (not numeric) it runs the image once to resolve it via `id`. When the
// resolved uid is 0 (root) chownNeeded is false — a root process already writes
// a root-owned volume dir, so no chown is required.
func imageRunUserGID(ctx context.Context, image string) (uid, gid int, chownNeeded bool, err error) {
	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Config.User}}", image).Output()
	if err != nil {
		return 0, 0, false, fmt.Errorf("inspect image user: %w", err)
	}
	user := strings.TrimSpace(string(out))
	if u, g, ok := parseUserGID(user); ok {
		return u, g, u != 0, nil
	}
	// Named user: resolve numerically by running the image as its own user.
	idOut, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "", image, "sh", "-c", "id -u; id -g").Output()
	if err != nil {
		return 0, 0, false, fmt.Errorf("resolve image user %q: %w", user, err)
	}
	fields := strings.Fields(string(idOut))
	if len(fields) < 2 {
		return 0, 0, false, fmt.Errorf("unexpected id output for image %q: %q", image, idOut)
	}
	u, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, false, fmt.Errorf("parse uid %q: %w", fields[0], err)
	}
	g, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, false, fmt.Errorf("parse gid %q: %w", fields[1], err)
	}
	return u, g, u != 0, nil
}

// chownVolumeToImageUser chowns a freshly-created volume dir so a non-root
// container image can write into its bind-mounted workspace. Best-effort: any
// failure is logged and swallowed so a deploy is never blocked by it. Only call
// this when the dir was just created (empty) — chowning an existing populated
// volume on every redeploy would be needlessly expensive and could clobber the
// ownership of files the container itself wrote.
func chownVolumeToImageUser(ctx context.Context, dir, image string, logFn func(string)) {
	uid, gid, need, err := imageRunUserGID(ctx, image)
	if err != nil {
		logFn(fmt.Sprintf("skip volume chown (%s): %v", dir, err))
		return
	}
	if !need {
		return
	}
	if err := chownRecursive(dir, uid, gid); err != nil {
		logFn(fmt.Sprintf("volume chown %s -> %d:%d failed: %v", dir, uid, gid, err))
		return
	}
	logFn(fmt.Sprintf("Set volume owner %s -> %d:%d (image runs as non-root)", dir, uid, gid))
}

// chownRecursive chowns root and everything under it. Recursive so that data
// copied in by the one-shot legacy-volume migration (written as root) also ends
// up owned by the image's user.
func chownRecursive(root string, uid, gid int) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
}
