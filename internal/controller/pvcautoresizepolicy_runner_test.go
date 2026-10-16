/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Runner", func() {
	Describe("Trigger", func() {
		It("never blocks, even when called repeatedly", func() {
			r := NewRunner(k8sClient, time.Hour, false)

			done := make(chan struct{})
			go func() {
				defer close(done)
				for range 10 {
					r.Trigger()
				}
			}()

			Eventually(done, time.Second).Should(BeClosed())

			// At most one signal is ever queued, regardless of
			// how many times Trigger() was called.
			Expect(r.trigger).To(HaveLen(1))
		})

		It("coalesces multiple calls into a single pending signal", func() {
			r := NewRunner(k8sClient, time.Hour, false)
			Expect(r.trigger).To(BeEmpty())

			r.Trigger()
			Expect(r.trigger).To(HaveLen(1))

			// Extra calls while one is already pending are dropped, not queued.
			r.Trigger()
			r.Trigger()
			r.Trigger()
			Expect(r.trigger).To(HaveLen(1))

			// Draining it and triggering again.
			<-r.trigger
			Expect(r.trigger).To(BeEmpty())
			r.Trigger()
			Expect(r.trigger).To(HaveLen(1))
		})
	})
})
