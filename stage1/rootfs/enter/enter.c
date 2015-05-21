// Copyright 2014 The rkt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <sched.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

static int errornum;
#define exit_if(_cond, _fmt, _args...)				\
	errornum++;						\
	if(_cond) {						\
		fprintf(stderr, _fmt "\n", ##_args);		\
		exit(errornum);					\
	}
#define pexit_if(_cond, _fmt, _args...)				\
	exit_if(_cond, _fmt ": %s", ##_args, strerror(errno))

static int openpidfd(int pid, char *which) {
	char	path[PATH_MAX];
	int	fd;
	exit_if(snprintf(path, sizeof(path),
			 "/proc/%i/%s", pid, which) == sizeof(path),
		"Path overflow");
	pexit_if((fd = open(path, O_RDONLY|O_CLOEXEC)) == -1,
		"Unable to open \"%s\"", path);
	return fd;
}

static int get_ppid() {
	FILE	*fp;
	int	ppid;
	char	proc_pid[PATH_MAX];

	/* We start in the pod root, where "ppid" should be. */

	pexit_if((fp = fopen("ppid", "r")) == NULL && errno != ENOENT,
		"Unable to open ppid file");

	/* The ppid file might not be written yet. The error is not fatal: we
	 * can try a bit later. */
	if (fp == NULL)
		return 0;

	pexit_if(fscanf(fp, "%i", &ppid) != 1,
		"Unable to read ppid");
	fclose(fp);

	/* Check if ppid terminated. It's ok if it terminates just after the
	 * check: it will be detected after. But in the common case, we get a
	 * better error message. */
	(void) snprintf(proc_pid, PATH_MAX, "/proc/%i", ppid);
	pexit_if(access(proc_pid, F_OK) != 0,
		"The pod has terminated (ppid=%i)", ppid);

	return ppid;
}

static int pod_is_running() {
	/* TODO(alban): check if the lock on the directory is taken... */
	return 1;
}

static int get_pid(void) {
	FILE	*fp;
	int	ppid = 0, pid = 0;
	int 	ret;
	char	proc_children[PATH_MAX];
	char	proc_exe1[PATH_MAX];
	char	proc_exe2[PATH_MAX];
	char	link1[PATH_MAX];
	char	link2[PATH_MAX];

	do {
		pexit_if((ppid = get_ppid()) == -1,
			"Unable to get ppid");

		if (ppid > 0)
			break;

		if (ppid == 0 && pod_is_running()) {
			usleep(100 * 1000);
			continue;
		}
	} while (pod_is_running());

	pexit_if(access("/proc/1/task/1/children", F_OK) != 0,
		"Unable to read /proc/1/task/1/children. Does your kernel have CONFIG_CHECKPOINT_RESTORE?");

	(void) snprintf(proc_children, PATH_MAX, "/proc/%i/task/%i/children", ppid, ppid);

	do {
		pexit_if((fp = fopen(proc_children, "r")) == NULL,
			"Unable to open '%s'", proc_children);

		errno = 0;
		ret = fscanf(fp, "%i ", &pid);
		pexit_if(errno != 0,
			"Unable to find children of process %i", ppid);
		fclose(fp);

		if (ret == 1)
			break;

		if (ret == 0) {
			usleep(100 * 1000);
			continue;
		}
	} while (pod_is_running());

	if (pid <= 0)
		return pid;

	/* Ok, we have the correct ppid and pid.
	 *
	 * But /sbin/init in the pod might not have been exec()ed yet and so it
	 * might not have done the chroot() yet. Wait until the pod is ready
	 * to be entered, otherwise we might chroot() to the wrong directory!
	 */
	(void) snprintf(proc_exe1, PATH_MAX, "/proc/%i/exe", ppid);
	(void) snprintf(proc_exe2, PATH_MAX, "/proc/%i/exe", pid);
	do {
		pexit_if(readlink(proc_exe1, link1, PATH_MAX) == -1,
			"Cannot read link '%s'", proc_exe1);
		pexit_if(readlink(proc_exe2, link2, PATH_MAX) == -1,
			"Cannot read link '%s'", proc_exe1);
		if (strcmp(link1, link2) == 0) {
			usleep(100 * 1000);
			continue;
		}
	} while (0);

	return pid;
}

int main(int argc, char *argv[])
{
	int	fd;
	int	pid;
	pid_t	child;
	int	status;
	int	root_fd;

	/* The parameters list is part of stage1 ABI and is specified in
	 * Documentation/devel/stage1-implementors-guide.md */
	exit_if(argc < 3,
		"Usage: %s imageid cmd [args...]", argv[0])

	pexit_if((pid = get_pid()) == -1,
		"Unable to get the pod process leader");

	root_fd = openpidfd(pid, "root");

#define ns(_typ, _nam)							\
	fd = openpidfd(pid, _nam);					\
	pexit_if(setns(fd, _typ), "Unable to enter " _nam " namespace");

#if 0
	/* TODO(vc): Nspawn isn't employing CLONE_NEWUSER, disabled for now */
	ns(CLONE_NEWUSER, "ns/user");
#endif
	ns(CLONE_NEWIPC,  "ns/ipc");
	ns(CLONE_NEWUTS,  "ns/uts");
	ns(CLONE_NEWNET,  "ns/net");
	ns(CLONE_NEWPID,  "ns/pid");
	ns(CLONE_NEWNS,	  "ns/mnt");

	pexit_if(fchdir(root_fd) < 0,
		"Unable to chdir to pod root");
	pexit_if(chroot(".") < 0,
		"Unable to chroot");
	pexit_if(close(root_fd) == -1,
		"Unable to close root_fd");

	/* Fork is required to realize consequence of CLONE_NEWPID */
	pexit_if(((child = fork()) == -1),
		"Unable to fork");

/* some stuff make the argv->args copy less cryptic */
#define ENTER_ARGV_FWD_OFFSET		2
#define DIAGEXEC_ARGV_FWD_OFFSET	6
#define args_fwd_idx(_idx) \
	((_idx - ENTER_ARGV_FWD_OFFSET) + DIAGEXEC_ARGV_FWD_OFFSET)

	if(child == 0) {
		char		root[PATH_MAX];
		char		env[PATH_MAX];
		char		*args[args_fwd_idx(argc) + 1 /* NULL terminator */];
		int		i;

		/* Child goes on to execute /diagexec */

		exit_if(snprintf(root, sizeof(root),
				 "/opt/stage2/%s/rootfs", argv[1]) == sizeof(root),
			"Root path overflow");

		exit_if(snprintf(env, sizeof(env),
				 "/rkt/env/%s", argv[1]) == sizeof(env),
			"Env path overflow");

		args[0] = "/diagexec";
		args[1] = root;
		args[2] = "/";	/* TODO(vc): plumb this into app.WorkingDirectory */
		args[3] = env;
		args[4] = "0"; /* uid */
		args[5] = "0"; /* gid */
		for(i = ENTER_ARGV_FWD_OFFSET; i < argc; i++) {
			args[args_fwd_idx(i)] = argv[i];
		}
		args[args_fwd_idx(i)] = NULL;

		pexit_if(execv(args[0], args) == -1,
			"Exec failed");
	}

	/* Wait for child, nsenter-like */
	for(;;) {
		if(waitpid(child, &status, WUNTRACED) == pid &&
		   (WIFSTOPPED(status))) {
			kill(getpid(), SIGSTOP);
			/* the above stops us, upon receiving SIGCONT we'll
			 * continue here and inform our child */
			kill(child, SIGCONT);
		} else {
			break;
		}
	}

	if(WIFEXITED(status)) {
		exit(WEXITSTATUS(status));
	} else if(WIFSIGNALED(status)) {
		kill(getpid(), WTERMSIG(status));
	}

	return EXIT_FAILURE;
}
