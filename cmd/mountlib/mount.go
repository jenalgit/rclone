package mountlib

import (
	"io"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/ncw/rclone/cmd"
	"github.com/ncw/rclone/fs"
	"github.com/ncw/rclone/fs/config/flags"
	"github.com/ncw/rclone/vfs"
	"github.com/ncw/rclone/vfs/vfsflags"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// Options set by command line flags
var (
	DebugFUSE                        = false
	AllowNonEmpty                    = false
	AllowRoot                        = false
	AllowOther                       = false
	DefaultPermissions               = false
	WritebackCache                   = false
	Daemon                           = false
	MaxReadAhead       fs.SizeSuffix = 128 * 1024
	ExtraOptions       []string
	ExtraFlags         []string
	AttrTimeout        = 0 * time.Second // how long the kernel caches attribute for
)

// Check is folder is empty
func checkMountEmpty(mountpoint string) error {
	fp, fpErr := os.Open(mountpoint)

	if fpErr != nil {
		return errors.Wrap(fpErr, "Can not open: "+mountpoint)
	}
	defer fs.CheckClose(fp, &fpErr)

	_, fpErr = fp.Readdirnames(1)

	// directory is not empty
	if fpErr != io.EOF {
		var e error
		var errorMsg = "Directory is not empty: " + mountpoint + " If you want to mount it anyway use: --allow-non-empty option"
		if fpErr == nil {
			e = errors.New(errorMsg)
		} else {
			e = errors.Wrap(fpErr, errorMsg)
		}
		return e
	}
	return nil
}

// NewMountCommand makes a mount command with the given name and Mount function
func NewMountCommand(commandName string, Mount func(f fs.Fs, mountpoint string) error) *cobra.Command {
	var commandDefintion = &cobra.Command{
		Use:   commandName + " remote:path /path/to/mountpoint",
		Short: `Mount the remote as a mountpoint. **EXPERIMENTAL**`,
		Long: `
rclone ` + commandName + ` allows Linux, FreeBSD, macOS and Windows to
mount any of Rclone's cloud storage systems as a file system with
FUSE.

This is **EXPERIMENTAL** - use with care.

First set up your remote using ` + "`rclone config`" + `.  Check it works with ` + "`rclone ls`" + ` etc.

Start the mount like this

    rclone ` + commandName + ` remote:path/to/files /path/to/local/mount

Or on Windows like this where X: is an unused drive letter

    rclone ` + commandName + ` remote:path/to/files X:

When the program ends, either via Ctrl+C or receiving a SIGINT or SIGTERM signal,
the mount is automatically stopped.

The umount operation can fail, for example when the mountpoint is busy.
When that happens, it is the user's responsibility to stop the mount manually with

    # Linux
    fusermount -u /path/to/local/mount
    # OS X
    umount /path/to/local/mount

### Installing on Windows

To run rclone ` + commandName + ` on Windows, you will need to
download and install [WinFsp](http://www.secfs.net/winfsp/).

WinFsp is an [open source](https://github.com/billziss-gh/winfsp)
Windows File System Proxy which makes it easy to write user space file
systems for Windows.  It provides a FUSE emulation layer which rclone
uses combination with
[cgofuse](https://github.com/billziss-gh/cgofuse).  Both of these
packages are by Bill Zissimopoulos who was very helpful during the
implementation of rclone ` + commandName + ` for Windows.

#### Windows caveats

Note that drives created as Administrator are not visible by other
accounts (including the account that was elevated as
Administrator). So if you start a Windows drive from an Administrative
Command Prompt and then try to access the same drive from Explorer
(which does not run as Administrator), you will not be able to see the
new drive.

The easiest way around this is to start the drive from a normal
command prompt. It is also possible to start a drive from the SYSTEM
account (using [the WinFsp.Launcher
infrastructure](https://github.com/billziss-gh/winfsp/wiki/WinFsp-Service-Architecture))
which creates drives accessible for everyone on the system or
alternatively using [the nssm service manager](https://nssm.cc/usage).

### Limitations

Without the use of "--vfs-cache-mode" this can only write files
sequentially, it can only seek when reading.  This means that many
applications won't work with their files on an rclone mount without
"--vfs-cache-mode writes" or "--vfs-cache-mode full".  See the [File
Caching](#file-caching) section for more info.

The bucket based remotes (eg Swift, S3, Google Compute Storage, B2,
Hubic) won't work from the root - you will need to specify a bucket,
or a path within the bucket.  So ` + "`swift:`" + ` won't work whereas
` + "`swift:bucket`" + ` will as will ` + "`swift:bucket/path`" + `.
None of these support the concept of directories, so empty
directories will have a tendency to disappear once they fall out of
the directory cache.

Only supported on Linux, FreeBSD, OS X and Windows at the moment.

### rclone ` + commandName + ` vs rclone sync/copy

File systems expect things to be 100% reliable, whereas cloud storage
systems are a long way from 100% reliable. The rclone sync/copy
commands cope with this with lots of retries.  However rclone ` + commandName + `
can't use retries in the same way without making local copies of the
uploads. Look at the **EXPERIMENTAL** [file caching](#file-caching)
for solutions to make ` + commandName + ` mount more reliable.

### Attribute caching

You can use the flag --attr-timeout to set the time the kernel caches
the attributes (size, modification time etc) for directory entries.

The default is 0s - no caching - which is recommended for filesystems
which can change outside the control of the kernel.

If you set it higher ('1s' or '1m' say) then the kernel will call back
to rclone less often making it more efficient, however there may be
strange effects when files change on the remote.

This is the same as setting the attr_timeout option in mount.fuse.

### Filters

Note that all the rclone filters can be used to select a subset of the
files to be visible in the mount.

### systemd

When running rclone ` + commandName + ` as a systemd service, it is possible
to use Type=notify. In this case the service will enter the started state
after the mountpoint has been successfully set up.
Units having the rclone ` + commandName + ` service specified as a requirement
will see all files and folders immediately in this mode.
` + vfs.Help,
		Run: func(command *cobra.Command, args []string) {
			cmd.CheckArgs(2, 2, command, args)
			fdst := cmd.NewFsDst(args)

			// Show stats if the user has specifically requested them
			if cmd.ShowStats() {
				stopStats := cmd.StartStats()
				defer close(stopStats)
			}

			// Skip checkMountEmpty if --allow-non-empty flag is used or if
			// the Operating System is Windows
			if !AllowNonEmpty && runtime.GOOS != "windows" {
				err := checkMountEmpty(args[1])
				if err != nil {
					log.Fatalf("Fatal error: %v", err)
				}
			}

			// Start background task if --background is specified
			if Daemon {
				daemonized := startBackgroundMode()
				if daemonized {
					return
				}
			}

			err := Mount(fdst, args[1])
			if err != nil {
				log.Fatalf("Fatal error: %v", err)
			}
		},
	}

	// Register the command
	cmd.Root.AddCommand(commandDefintion)

	// Add flags
	flagSet := commandDefintion.Flags()
	flags.BoolVarP(flagSet, &DebugFUSE, "debug-fuse", "", DebugFUSE, "Debug the FUSE internals - needs -v.")
	// mount options
	flags.BoolVarP(flagSet, &AllowNonEmpty, "allow-non-empty", "", AllowNonEmpty, "Allow mounting over a non-empty directory.")
	flags.BoolVarP(flagSet, &AllowRoot, "allow-root", "", AllowRoot, "Allow access to root user.")
	flags.BoolVarP(flagSet, &AllowOther, "allow-other", "", AllowOther, "Allow access to other users.")
	flags.BoolVarP(flagSet, &DefaultPermissions, "default-permissions", "", DefaultPermissions, "Makes kernel enforce access control based on the file mode.")
	flags.BoolVarP(flagSet, &WritebackCache, "write-back-cache", "", WritebackCache, "Makes kernel buffer writes before sending them to rclone. Without this, writethrough caching is used.")
	flags.FVarP(flagSet, &MaxReadAhead, "max-read-ahead", "", "The number of bytes that can be prefetched for sequential reads.")
	flags.DurationVarP(flagSet, &AttrTimeout, "attr-timeout", "", AttrTimeout, "Time for which file/directory attributes are cached.")
	flags.StringArrayVarP(flagSet, &ExtraOptions, "option", "o", []string{}, "Option for libfuse/WinFsp. Repeat if required.")
	flags.StringArrayVarP(flagSet, &ExtraFlags, "fuse-flag", "", []string{}, "Flags or arguments to be passed direct to libfuse/WinFsp. Repeat if required.")
	flags.BoolVarP(flagSet, &Daemon, "daemon", "", Daemon, "Run mount as a daemon (background mode).")

	// Add in the generic flags
	vfsflags.AddFlags(flagSet)

	return commandDefintion
}
