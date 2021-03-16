/*
 * Copyright ©2020. The virtual-kubelet authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package multischeduler

import (
	"context"
	"fmt"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	schedulerserverconfig "k8s.io/kubernetes/cmd/kube-scheduler/app/config"
	"k8s.io/kubernetes/pkg/scheduler/profile"

	eventsv1beta1 "k8s.io/api/events/v1beta1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	schedulerappconfig "k8s.io/kubernetes/cmd/kube-scheduler/app/config"
	"k8s.io/kubernetes/pkg/scheduler"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
)

// Scheduler define the scheduler struct
type Scheduler struct {
	*scheduler.Scheduler
	// configuration of scheduler
	Config schedulerappconfig.Config
	// stop signal
	StopCh <-chan struct{}
}

// NewScheduler executes the scheduler based on the given configuration. It only return on error or when stopCh is closed.
func NewScheduler(ctx context.Context, cc schedulerappconfig.Config, stopCh <-chan struct{}) (*Scheduler, error) {
	// To help debugging, immediately log version
	outOfTreeRegistry := make(framework.Registry)
	completedConfig := cc.Complete()
	recordFactory := getRecorderFactory(&completedConfig)

	// Create the scheduler.
	sched, err := scheduler.New(cc.Client,
		cc.InformerFactory,
		cc.PodInformer,
		recordFactory,
		stopCh,
		scheduler.WithProfiles(cc.ComponentConfig.Profiles...),
		scheduler.WithAlgorithmSource(cc.ComponentConfig.AlgorithmSource),
		scheduler.WithPreemptionDisabled(cc.ComponentConfig.DisablePreemption),
		scheduler.WithPercentageOfNodesToScore(cc.ComponentConfig.PercentageOfNodesToScore),
		scheduler.WithBindTimeoutSeconds(cc.ComponentConfig.BindTimeoutSeconds),
		scheduler.WithFrameworkOutOfTreeRegistry(outOfTreeRegistry),
		scheduler.WithPodMaxBackoffSeconds(cc.ComponentConfig.PodMaxBackoffSeconds),
		scheduler.WithPodInitialBackoffSeconds(cc.ComponentConfig.PodInitialBackoffSeconds),
		scheduler.WithExtenders(cc.ComponentConfig.Extenders...),
	)
	if err != nil {
		return nil, err
	}
	return &Scheduler{
		Config: cc, Scheduler: sched, StopCh: stopCh,
	}, nil
}

// Run executes the scheduler based on the given configuration. It only return on error or when stopCh is closed.
func (sched *Scheduler) Run(ctx context.Context) error {
	// Prepare the event broadcaster.
	if sched.Config.Broadcaster != nil && sched.Config.EventClient != nil {
		sched.Config.Broadcaster.StartRecordingToSink(sched.StopCh)
	}

	// Start all informers.
	go sched.Config.PodInformer.Informer().Run(sched.StopCh)
	sched.Config.InformerFactory.Start(sched.StopCh)

	// Wait for all caches to sync before scheduling.
	sched.Config.InformerFactory.WaitForCacheSync(sched.StopCh)

	if !cache.WaitForCacheSync(ctx.Done()) {
		return fmt.Errorf("failed to wait cache sync")
	}
	<-sched.StopCh
	return nil
}

func getRecorderFactory(cc *schedulerserverconfig.CompletedConfig) profile.RecorderFactory {
	if _, err := cc.Client.Discovery().ServerResourcesForGroupVersion(eventsv1beta1.SchemeGroupVersion.String()); err == nil {
		cc.Broadcaster = events.NewBroadcaster(&events.EventSinkImpl{Interface: cc.EventClient.Events("")})
		return profile.NewRecorderFactory(cc.Broadcaster)
	}
	return func(name string) events.EventRecorder {
		r := cc.CoreBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: name})
		return record.NewEventRecorderAdapter(r)
	}
}
