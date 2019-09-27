/*
Copyright 2018 The Knative Authors

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
package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Shopify/sarama"
	"github.com/google/go-cmp/cmp"
	"go.uber.org/zap"

	"github.com/cloudevents/sdk-go"
	"knative.dev/eventing-contrib/kafka/channel/pkg/utils"
	"knative.dev/eventing-contrib/kafka/common/pkg/kafka"
	eventingduck "knative.dev/eventing/pkg/apis/duck/v1alpha1"
	channels "knative.dev/eventing/pkg/channel"
	"knative.dev/eventing/pkg/channel/multichannelfanout"
)

type KafkaDispatcher struct {
	// TODO: config doesn't have to be atomic as it is read and updated using updateLock.
	config           atomic.Value
	hostToChannelMap atomic.Value
	// hostToChannelMapLock is used to update hostToChannelMap
	hostToChannelMapLock sync.Mutex

	receiver   *channels.EventReceiver
	dispatcher *channels.EventDispatcher

	kafkaAsyncProducer  sarama.AsyncProducer
	kafkaConsumerGroups map[channels.ChannelReference]map[subscription]sarama.ConsumerGroup
	// consumerUpdateLock must be used to update kafkaConsumers
	consumerUpdateLock   sync.Mutex
	kafkaConsumerFactory kafka.KafkaConsumerGroupFactory

	topicFunc TopicFunc
	logger    *zap.Logger
}

type TopicFunc func(separator, namespace, name string) string

type KafkaDispatcherArgs struct {
	ClientID  string
	Brokers   []string
	TopicFunc TopicFunc
	Logger    *zap.Logger
}

type consumerMessageHandler struct {
	sub        subscription
	dispatcher *channels.EventDispatcher
}

func (c consumerMessageHandler) Handle(ctx context.Context, message *sarama.ConsumerMessage) (bool, error) {
	event := fromKafkaMessage(message)
	return true, c.dispatcher.DispatchEvent(ctx, *event, c.sub.SubscriberURI, c.sub.ReplyURI)
}

var _ kafka.KafkaConsumerHandler = (*consumerMessageHandler)(nil)

type subscription struct {
	UID           string
	SubscriberURI string
	ReplyURI      string
}

// configDiff diffs the new config with the existing config. If there are no differences, then the
// empty string is returned. If there are differences, then a non-empty string is returned
// describing the differences.
func (d *KafkaDispatcher) configDiff(updated *multichannelfanout.Config) string {
	return cmp.Diff(d.getConfig(), updated)
}

// UpdateKafkaConsumers will be called by new CRD based kafka channel dispatcher controller.
func (d *KafkaDispatcher) UpdateKafkaConsumers(config *multichannelfanout.Config) (map[eventingduck.SubscriberSpec]error, error) {
	if config == nil {
		return nil, fmt.Errorf("nil config")
	}

	d.consumerUpdateLock.Lock()
	defer d.consumerUpdateLock.Unlock()

	newSubs := make(map[subscription]bool)
	failedToSubscribe := make(map[eventingduck.SubscriberSpec]error)
	for _, cc := range config.ChannelConfigs {
		channelRef := channels.ChannelReference{
			Name:      cc.Name,
			Namespace: cc.Namespace,
		}
		for _, subSpec := range cc.FanoutConfig.Subscriptions {
			sub := newSubscription(subSpec)
			if _, ok := d.kafkaConsumerGroups[channelRef][sub]; !ok {
				// only subscribe when not exists in channel-subscriptions map
				// do not need to resubscribe every time channel fanout config is updated
				if err := d.subscribe(channelRef, sub); err != nil {
					failedToSubscribe[subSpec] = err
				}
			}
			newSubs[sub] = true
		}
	}

	// Unsubscribe and close consumer for any deleted subscriptions
	for channelRef, subMap := range d.kafkaConsumerGroups {
		for sub := range subMap {
			if ok := newSubs[sub]; !ok {
				d.unsubscribe(channelRef, sub)
			}
		}
	}
	return failedToSubscribe, nil
}

// UpdateHostToChannelMap will be called by new CRD based kafka channel dispatcher controller.
func (d *KafkaDispatcher) UpdateHostToChannelMap(config *multichannelfanout.Config) error {
	if config == nil {
		return errors.New("nil config")
	}

	d.hostToChannelMapLock.Lock()
	defer d.hostToChannelMapLock.Unlock()

	hcMap, err := createHostToChannelMap(config)
	if err != nil {
		return err
	}

	d.setHostToChannelMap(hcMap)
	return nil
}

func createHostToChannelMap(config *multichannelfanout.Config) (map[string]channels.ChannelReference, error) {
	hcMap := make(map[string]channels.ChannelReference, len(config.ChannelConfigs))
	for _, cConfig := range config.ChannelConfigs {
		if cr, ok := hcMap[cConfig.HostName]; ok {
			return nil, fmt.Errorf(
				"duplicate hostName found. Each channel must have a unique host header. HostName:%s, channel:%s.%s, channel:%s.%s",
				cConfig.HostName,
				cConfig.Namespace,
				cConfig.Name,
				cr.Namespace,
				cr.Name)
		}
		hcMap[cConfig.HostName] = channels.ChannelReference{Name: cConfig.Name, Namespace: cConfig.Namespace}
	}
	return hcMap, nil
}

// Start starts the kafka dispatcher's message processing.
func (d *KafkaDispatcher) Start(ctx context.Context) error {
	if d.receiver == nil {
		return fmt.Errorf("message receiver is not set")
	}

	if d.kafkaAsyncProducer == nil {
		return fmt.Errorf("kafkaAsyncProducer is not set")
	}

	go func() {
		for {
			select {
			case e := <-d.kafkaAsyncProducer.Errors():
				d.logger.Warn("Got", zap.Error(e))
			case s := <-d.kafkaAsyncProducer.Successes():
				d.logger.Info("Sent", zap.Any("success", s))
			case <-ctx.Done():
				return
			}
		}
	}()

	return d.receiver.Start(ctx)
}

// subscribe reads kafkaConsumers which gets updated in UpdateConfig in a separate go-routine.
// subscribe must be called under updateLock.
func (d *KafkaDispatcher) subscribe(channelRef channels.ChannelReference, sub subscription) error {
	d.logger.Info("Subscribing", zap.Any("channelRef", channelRef), zap.Any("subscription", sub))

	topicName := d.topicFunc(utils.KafkaChannelSeparator, channelRef.Namespace, channelRef.Name)
	groupID := fmt.Sprintf("kafka.%s", sub.UID)

	handler := consumerMessageHandler{sub, d.dispatcher}

	consumerGroup, err := d.kafkaConsumerFactory.StartConsumerGroup(groupID, []string{topicName}, d.logger, handler)

	if err != nil {
		// we can not create a consumer - logging that, with reason
		d.logger.Info("Could not create proper consumer", zap.Error(err))
		return err
	}

	// sarama reports error in consumerGroup.Error() channel
	// this goroutine logs errors incoming
	go func() {
		for err = range consumerGroup.Errors() {
			d.logger.Warn("Error in consumer group", zap.Error(err))
		}
	}()

	consumerGroupMap, ok := d.kafkaConsumerGroups[channelRef]
	if !ok {
		consumerGroupMap = make(map[subscription]sarama.ConsumerGroup)
		d.kafkaConsumerGroups[channelRef] = consumerGroupMap
	}
	consumerGroupMap[sub] = consumerGroup

	return nil
}

// unsubscribe reads kafkaConsumers which gets updated in UpdateConfig in a separate go-routine.
// unsubscribe must be called under updateLock.
func (d *KafkaDispatcher) unsubscribe(channel channels.ChannelReference, sub subscription) error {
	d.logger.Info("Unsubscribing from channel", zap.Any("channel", channel), zap.Any("subscription", sub))
	if consumer, ok := d.kafkaConsumerGroups[channel][sub]; ok {
		delete(d.kafkaConsumerGroups[channel], sub)
		return consumer.Close()
	}
	return nil
}
func (d *KafkaDispatcher) getConfig() *multichannelfanout.Config {
	return d.config.Load().(*multichannelfanout.Config)
}

func (d *KafkaDispatcher) setConfig(config *multichannelfanout.Config) {
	d.config.Store(config)
}

func (d *KafkaDispatcher) getHostToChannelMap() map[string]channels.ChannelReference {
	return d.hostToChannelMap.Load().(map[string]channels.ChannelReference)
}

func (d *KafkaDispatcher) setHostToChannelMap(hcMap map[string]channels.ChannelReference) {
	d.hostToChannelMap.Store(hcMap)
}

func NewDispatcher(args *KafkaDispatcherArgs) (*KafkaDispatcher, error) {
	conf := sarama.NewConfig()
	conf.Version = sarama.V2_0_0_0
	conf.ClientID = args.ClientID
	conf.Consumer.Return.Errors = true // Returns the errors in ConsumerGroup#Errors() https://godoc.org/github.com/Shopify/sarama#ConsumerGroup
	client, err := sarama.NewClient(args.Brokers, conf)

	if err != nil {
		return nil, fmt.Errorf("unable to create kafka client: %v", err)
	}

	producer, err := sarama.NewAsyncProducerFromClient(client)
	if err != nil {
		return nil, fmt.Errorf("unable to create kafka producer: %v", err)
	}

	dispatcher := &KafkaDispatcher{
		dispatcher:           channels.NewEventDispatcher(args.Logger),
		kafkaConsumerFactory: kafka.NewConsumerGroupFactory(client),
		kafkaConsumerGroups:  make(map[channels.ChannelReference]map[subscription]sarama.ConsumerGroup),
		kafkaAsyncProducer:   producer,
		logger:               args.Logger,
	}
	receiverFunc, err := channels.NewEventReceiver(
		func(ctx context.Context, channel channels.ChannelReference, event cloudevents.Event) error {
			dispatcher.kafkaAsyncProducer.Input() <- toKafkaMessage(ctx, channel, &event, args.TopicFunc)
			return nil
		},
		args.Logger,
		channels.ResolveChannelFromHostHeader(channels.ResolveChannelFromHostFunc(dispatcher.getChannelReferenceFromHost)))
	if err != nil {
		return nil, err
	}
	dispatcher.receiver = receiverFunc
	dispatcher.setConfig(&multichannelfanout.Config{})
	dispatcher.setHostToChannelMap(map[string]channels.ChannelReference{})
	dispatcher.topicFunc = args.TopicFunc
	return dispatcher, nil
}

func (d *KafkaDispatcher) getChannelReferenceFromHost(host string) (channels.ChannelReference, error) {
	chMap := d.getHostToChannelMap()
	cr, ok := chMap[host]
	if !ok {
		return cr, fmt.Errorf("invalid Hostname:%s. Hostname not found in ConfigMap for any Channel", host)
	}
	return cr, nil
}

func fromKafkaMessage(kafkaMessage *sarama.ConsumerMessage) *cloudevents.Event {
	var event = &cloudevents.Event{}
	for _, header := range kafkaMessage.Headers {
		h := string(header.Key)
		v := string(header.Value)
		switch h {
		case "ce_datacontenttype":
			event.SetDataContentType(v)
		case "ce_specversion":
			event.SetSpecVersion(v)
		case "ce_type":
			event.SetType(v)
		case "ce_source":
			event.SetSource(v)
		case "ce_id":
			event.SetID(v)
		case "ce_time":
			t, _ := time.Parse(time.RFC3339, v)
			event.SetTime(t)
		case "ce_subject":
			event.SetSubject(v)
		case "ce_dataschema":
			event.SetDataSchema(v)
		default:
			// Extensions
			event.SetExtension(h, v)
		}
	}
	return event
}

func toKafkaMessage(ctx context.Context, channel channels.ChannelReference, event *cloudevents.Event, topicFunc TopicFunc) *sarama.ProducerMessage {
	data, _ := event.DataBytes()
	kafkaMessage := sarama.ProducerMessage{
		Topic: topicFunc(utils.KafkaChannelSeparator, channel.Namespace, channel.Name),
		Value: sarama.ByteEncoder(data),
	}
	kafkaMessage.Headers = append(kafkaMessage.Headers, sarama.RecordHeader{Key: []byte("ce_specversion"), Value: []byte(event.SpecVersion())})
	kafkaMessage.Headers = append(kafkaMessage.Headers, sarama.RecordHeader{Key: []byte("ce_type"), Value: []byte(event.Type())})
	kafkaMessage.Headers = append(kafkaMessage.Headers, sarama.RecordHeader{Key: []byte("ce_source"), Value: []byte(event.Source())})
	kafkaMessage.Headers = append(kafkaMessage.Headers, sarama.RecordHeader{Key: []byte("ce_id"), Value: []byte(event.ID())})
	kafkaMessage.Headers = append(kafkaMessage.Headers, sarama.RecordHeader{Key: []byte("ce_time"), Value: []byte(event.Time().Format(time.RFC3339))})
	if event.DataContentType() != "" {
		kafkaMessage.Headers = append(kafkaMessage.Headers, sarama.RecordHeader{Key: []byte("ce_datacontenttype"), Value: []byte(event.DataContentType())})
	}
	if event.Subject() != "" {
		kafkaMessage.Headers = append(kafkaMessage.Headers, sarama.RecordHeader{Key: []byte("ce_subject"), Value: []byte(event.Subject())})
	}
	if event.DataSchema() != "" {
		kafkaMessage.Headers = append(kafkaMessage.Headers, sarama.RecordHeader{Key: []byte("ce_dataschema"), Value: []byte(event.DataSchema())})
	}
	// Only setting string extensions.
	for k, v := range event.Extensions() {
		if vs, ok := v.(string); ok {
			kafkaMessage.Headers = append(kafkaMessage.Headers, sarama.RecordHeader{Key: []byte("ce_" + k), Value: []byte(vs)})
		}
	}

	return &kafkaMessage
}

func newSubscription(spec eventingduck.SubscriberSpec) subscription {
	return subscription{
		UID:           string(spec.UID),
		SubscriberURI: spec.SubscriberURI,
		ReplyURI:      spec.ReplyURI,
	}
}
