package testprocessors

import (
	streamtypes "lunar/engine/streams/types"
)

const (
	GlobalKeyCacheHit = "cache_hit"

	cacheHitConditionName    = "cacheHit"
	cacheMissedConditionName = "cacheMissed"
)

func NewMockProcessorUsingCache(metadata *streamtypes.ProcessorMetaData) (streamtypes.Processor, error) { //nolint:lll
	return &MockProcessorUsingCache{Name: metadata.Name, Metadata: metadata}, nil
}

type MockProcessorUsingCache struct {
	Name     string
	Metadata *streamtypes.ProcessorMetaData
}

func (p *MockProcessorUsingCache) Execute(apiStream *streamtypes.APIStream) (streamtypes.ProcessorIO, error) { //nolint:lll
	signInExecution(apiStream, p.Name)
	if val, err := apiStream.Context.GetGlobalContext().Get(GlobalKeyCacheHit); err == nil {
		if val.(bool) {
			return streamtypes.ProcessorIO{
				Type: streamtypes.StreamTypeRequest,
				Name: cacheHitConditionName,
			}, nil
		}
	}
	return streamtypes.ProcessorIO{
		Type: streamtypes.StreamTypeRequest,
		Name: cacheMissedConditionName,
	}, nil
}

func (p *MockProcessorUsingCache) GetName() string {
	return p.Name
}
