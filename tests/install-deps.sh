#!/bin/bash -eu

# Helper script for Continuous Integration Services

if [ "${CI-}" == true ] ; then
	# https://semaphoreci.com/
	if [ "${SEMAPHORE-}" == true ] ; then
		# Most dependencies are already installed on Semaphore.
		# Here we can install any missing dependencies. Whenever
		# Semaphore installs more dependencies on their platform,
		# they should be removed from here to save time.

		sudo apt-get update -qq || true
		sudo apt-get install -y libcapture-tiny-perl # used by tools/

		# We need a more recent version of "go vet" to avoid the
		# following bug: https://github.com/golang/go/issues/6820
		wget https://storage.googleapis.com/golang/go1.4.2.linux-amd64.tar.gz -P /tmp
		sudo tar -C /usr/local -xzf /tmp/go1.4.2.linux-amd64.tar.gz
	fi

	# https://circleci.com/
	if [ "${CIRCLECI-}" == true ] ; then
		sudo apt-get update -qq
		sudo apt-get install -y libtool intltool # for ./autogen.sh
		sudo apt-get install -y cpio realpath squashfs-tools
		sudo apt-get install -y strace gdb libcap-ng-utils
		sudo apt-get install -y coreutils # systemd needs a recent /bin/ln
		sudo apt-get install -y gperf libcap-dev linux-headers-3.13.0-34-generic linux-libc-dev libseccomp-dev # systemd deps
		sudo apt-get install -y git # cloning a tag of systemd
	fi
fi
