package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/history"
	"go.uber.org/mock/gomock"
)

// newFileTracker returns a MockFileTracker that allows any number of
// calls, always reporting lastRead and no prior reads.
func newFileTracker(t *testing.T, lastRead time.Time) *MockFileTracker {
	t.Helper()
	m := NewMockFileTracker(gomock.NewController(t))
	m.EXPECT().RecordRead(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.EXPECT().LastReadTime(gomock.Any(), gomock.Any(), gomock.Any()).Return(lastRead).AnyTimes()
	m.EXPECT().ListReadFiles(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	return m
}

// newRecordingFileTracker is like newFileTracker but also captures the
// paths passed to RecordRead, in call order.
func newRecordingFileTracker(t *testing.T, lastRead time.Time) (*MockFileTracker, *[]string) {
	t.Helper()
	m := NewMockFileTracker(gomock.NewController(t))
	reads := &[]string{}
	m.EXPECT().RecordRead(gomock.Any(), gomock.Any(), gomock.Any()).
		Do(func(_ context.Context, _, path string) {
			*reads = append(*reads, path)
		}).AnyTimes()
	m.EXPECT().LastReadTime(gomock.Any(), gomock.Any(), gomock.Any()).Return(lastRead).AnyTimes()
	m.EXPECT().ListReadFiles(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	return m, reads
}

// newHistoryService returns a MockHistoryService whose
// GetByPathAndSession reports existing for any lookup, or a "not
// found" error when missing is true. Create and CreateVersion always
// succeed without recording what they were given.
func newHistoryService(t *testing.T, existing string, missing bool) *MockHistoryService {
	t.Helper()
	m := NewMockHistoryService(gomock.NewController(t))
	m.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, path, content string) (history.File, error) {
			return history.File{Path: path, Content: content}, nil
		}).AnyTimes()
	m.EXPECT().CreateVersion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(history.File{}, nil).AnyTimes()
	m.EXPECT().GetByPathAndSession(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, path, _ string) (history.File, error) {
			if missing {
				return history.File{}, errors.New("not found")
			}
			return history.File{Path: path, Content: existing}, nil
		}).AnyTimes()
	return m
}

// newRecordingHistoryService is like newHistoryService but also
// captures every content value passed to Create/CreateVersion, in call
// order, mirroring the old mockHistoryService.recorded() helper.
func newRecordingHistoryService(t *testing.T, existing string, missing bool) (*MockHistoryService, *[]string) {
	t.Helper()
	m := NewMockHistoryService(gomock.NewController(t))
	versions := &[]string{}
	m.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, path, content string) (history.File, error) {
			*versions = append(*versions, content)
			return history.File{Path: path, Content: content}, nil
		}).AnyTimes()
	m.EXPECT().CreateVersion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, content string) (history.File, error) {
			*versions = append(*versions, content)
			return history.File{}, nil
		}).AnyTimes()
	m.EXPECT().GetByPathAndSession(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, path, _ string) (history.File, error) {
			if missing {
				return history.File{}, errors.New("not found")
			}
			return history.File{Path: path, Content: existing}, nil
		}).AnyTimes()
	return m, versions
}
