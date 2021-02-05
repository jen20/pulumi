// Copyright 2016-2018, Pulumi Corporation.
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

package filestate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/pulumi/pulumi/pkg/v2/backend"
	"github.com/pulumi/pulumi/sdk/v2/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v2/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v2/go/common/util/contract"
	"github.com/pulumi/pulumi/sdk/v2/go/common/util/fsutil"
	"github.com/pulumi/pulumi/sdk/v2/go/common/workspace"
)

const pulumiFilestateLockingEnvVar = "PULUMI_FILESTATE_LOCKING"

type lockContent struct {
	Pid       int       `json:"pid"`
	Username  string    `json:"username"`
	Hostname  string    `json:"hostname"`
	Timestamp time.Time `json:"timestamp"`
}

func newLockContent() (*lockContent, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	return &lockContent{
		Pid:       os.Getpid(),
		Username:  u.Username,
		Hostname:  hostname,
		Timestamp: time.Now(),
	}, nil
}

// checkForLock looks for any existing locks for this stack, and returns a helpful diagnostic if there is one.
func (b *localBackend) checkForLock(ctxt context.Context, stackRef backend.StackReference) error {
	allFiles, err := listBucket(b.bucket, b.lockDir())
	if err != nil {
		return err
	}

	var lockKeys []string
	for _, file := range allFiles {
		if file.IsDir {
			continue
		}
		if b.isLockForThisStack(file.Key, stackRef) && file.Key != b.lockPath(stackRef.Name()) {
			lockKeys = append(lockKeys, file.Key)
		}
	}

	if len(lockKeys) > 0 {
		errorString := fmt.Sprintf("the stack is current locked by %v lock(s). Either wait for the other "+
			"process(es) to end or manually delete the lock file(s).", len(lockKeys))

		for _, lock := range lockKeys {
			content, err := b.bucket.ReadAll(ctxt, lock)
			if err != nil {
				return err
			}
			l := &lockContent{}
			err = json.Unmarshal(content, &l)
			if err != nil {
				return err
			}

			errorString += fmt.Sprintf("\n  %v: created by %v@%v (pid %v) at %v",
				b.url+"/"+lock,
				l.Username,
				l.Hostname,
				l.Pid,
				l.Timestamp.Format(time.RFC3339),
			)
		}

		return errors.New(errorString)
	}
	return nil
}

func (b *localBackend) Lock(ctx context.Context, stackRef backend.StackReference) error {
	//
	err := b.checkForLock(ctx, stackRef)
	if err != nil {
		return err
	}
	lockContent, err := newLockContent()
	if err != nil {
		return err
	}
	content, err := json.Marshal(lockContent)
	if err != nil {
		return err
	}
	err = b.bucket.WriteAll(ctx, b.lockPath(stackRef.Name()), content, nil)
	if err != nil {
		return err
	}
	err = b.checkForLock(ctx, stackRef)
	if err != nil {
		b.Unlock(ctx, stackRef)
		return err
	}
	return nil
}

func (b *localBackend) Unlock(ctx context.Context, stackRef backend.StackReference) {
	err := b.bucket.Delete(ctx, b.lockPath(stackRef.Name()))
	if err != nil {
		b.d.Errorf(
			diag.Message("", "there was a problem deleting the lock at %v, manual clean up may be required: %v"),
			path.Join(b.url, b.lockPath(stackRef.Name())),
			err)
	}
}

func (b *localBackend) lockDir() string {
	return path.Join(workspace.BookkeepingDir, workspace.LockDir)
}

func (b *localBackend) lockPrefix(stack tokens.QName) string {
	contract.Require(stack != "", "stack")
	return path.Join(b.lockDir(), fsutil.QnamePath(stack)+".")
}

func (b *localBackend) isLockForThisStack(file string, stackRef backend.StackReference) bool {
	return strings.HasPrefix(file, b.lockPrefix(stackRef.Name()))
}

func (b *localBackend) lockPath(stack tokens.QName) string {
	contract.Require(stack != "", "stack")
	return b.lockPrefix(stack) + b.lockID + ".json"
}