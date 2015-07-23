// Copyright 2015 Google Inc. All Rights Reserved.
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

package fuseops

import (
	"fmt"
	"log"
	"reflect"
	"strings"

	"github.com/jacobsa/fuse/internal/fuseshim"
	"github.com/jacobsa/reqtrace"
	"golang.org/x/net/context"
)

// An interface that all ops inside which commonOp is embedded must
// implement.
type internalOp interface {
	Op

	// Create a response message for the kernel, with leading pading for a
	// fusekernel.OutHeader struct.
	kernelResponse() []byte
}

// A function that sends a reply message back to the kernel for the request
// with the given fuse unique ID.
type replyFunc func(uint64, []byte) error

// A helper for embedding common behavior.
type commonOp struct {
	// The context exposed to the user.
	ctx context.Context

	// The op in which this struct is embedded.
	op internalOp

	// The fuse unique ID of this request, as assigned by the kernel.
	fuseID uint64

	// A function that can be used to send a reply to the kernel.
	sendReply replyFunc

	// A function that can be used to log debug information about the op. The
	// first argument is a call depth.
	//
	// May be nil.
	debugLog func(int, string, ...interface{})

	// A logger to be used for logging exceptional errors.
	//
	// May be nil.
	errorLogger *log.Logger
}

func (o *commonOp) ShortDesc() (desc string) {
	v := reflect.ValueOf(o.op)
	opName := v.Type().String()

	// Attempt to better handle the usual case: a string that looks like
	// "*fuseops.GetInodeAttributesOp".
	const prefix = "*fuseops."
	const suffix = "Op"
	if strings.HasPrefix(opName, prefix) && strings.HasSuffix(opName, suffix) {
		opName = opName[len(prefix) : len(opName)-len(suffix)]
	}

	// Include the inode number to which the op applies, if possible.
	if f := v.FieldByName("Inode"); f.IsValid() {
		desc = fmt.Sprintf("%s(inode=%v)", opName, f.Interface())
	}

	return
}

func (o *commonOp) init(
	ctx context.Context,
	op internalOp,
	bazilReq fuseshim.Request,
	debugLog func(int, string, ...interface{}),
	errorLogger *log.Logger,
	finished func(error)) {
	// Initialize basic fields.
	o.ctx = ctx
	o.op = op
	o.bazilReq = bazilReq
	o.debugLog = debugLog
	o.errorLogger = errorLogger
	o.finished = finished

	// Set up a trace span for this op.
	var reportForTrace reqtrace.ReportFunc
	o.ctx, reportForTrace = reqtrace.StartSpan(o.ctx, o.op.ShortDesc())

	// When the op is finished, report to both reqtrace and the connection.
	prevFinish := o.finished
	o.finished = func(err error) {
		reportForTrace(err)
		prevFinish(err)
	}
}

func (o *commonOp) Context() context.Context {
	return o.ctx
}

func (o *commonOp) Logf(format string, v ...interface{}) {
	if o.debugLog == nil {
		return
	}

	const calldepth = 2
	o.debugLog(calldepth, format, v...)
}

func (o *commonOp) Respond(err error) {
	// Report that the user is responding.
	o.finished(err)

	// If successful, we should respond to fuseshim with the appropriate struct.
	if err == nil {
		o.op.respond()
		return
	}

	// Log the error.
	if o.debugLog != nil {
		o.Logf(
			"-> (%s) error: %v",
			o.op.ShortDesc(),
			err)
	}

	if o.errorLogger != nil {
		o.errorLogger.Printf(
			"(%s) error: %v",
			o.op.ShortDesc(),
			err)
	}

	// Send a response to the kernel.
	o.bazilReq.RespondError(err)
}
