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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	autoresizev1alpha1 "github.com/csi-addons/pvc-autoresizer/api/v1alpha1"
)

var _ = Describe("PVCAutoResizePolicy Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}
		pvcautoresizepolicy := &autoresizev1alpha1.PVCAutoResizePolicy{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind PVCAutoResizePolicy")
			err := k8sClient.Get(ctx, typeNamespacedName, pvcautoresizepolicy)
			if err != nil && errors.IsNotFound(err) {
				resource := &autoresizev1alpha1.PVCAutoResizePolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
					},
					Spec: autoresizev1alpha1.PVCAutoResizePolicySpec{
						Provisioner: "example.csi.driver",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &autoresizev1alpha1.PVCAutoResizePolicy{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance PVCAutoResizePolicy")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should trigger the runner on reconcile", func() {
			By("Reconciling the created resource")
			resizer := NewRunner(k8sClient, 0, false)
			controllerReconciler := &PVCAutoResizePolicyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Runner: resizer,
			}

			// Reconcile should trigger the runner.
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resizer.trigger).To(HaveLen(1))

			// Multiple reconciles should not queue more objects.
			// Or, should not get blocked if channel is full.
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(resizer.trigger).To(HaveLen(1))
		})
	})
})
