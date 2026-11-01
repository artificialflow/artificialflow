package id

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Snowflake Generator Constants
const (
	nodeBits        = 10
	stepBits        = 12
	nodeMax         = -1 ^ (-1 << nodeBits)
	stepMax         = -1 ^ (-1 << stepBits)
	timeShift       = nodeBits + stepBits
	nodeShift       = stepBits
	epoch     int64 = 1704067200000 // 2024-01-01 00:00:00 UTC
)

type Node struct {
	mu        sync.Mutex
	timestamp int64
	node      int64
	step      int64
}

var defaultNode *Node
var once sync.Once

// NewNode creates a new Snowflake node
func NewNode(node int64) (*Node, error) {
	if node < 0 || node > nodeMax {
		return nil, errors.New("node number must be between 0 and 1023")
	}

	return &Node{
		timestamp: 0,
		node:      node,
		step:      0,
	}, nil
}

// Generate creates a new unique int64 ID
func (n *Node) Generate() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()

	now := time.Now().UnixMilli()

	if now < n.timestamp {
		// Clock regressed, refuse to generate ID (or wait)
		// For simplicity in this implementation, we just use the last timestamp
		now = n.timestamp
	}

	if now == n.timestamp {
		n.step = (n.step + 1) & stepMax
		if n.step == 0 {
			// Sequence exhausted, wait for next millisecond
			for now <= n.timestamp {
				now = time.Now().UnixMilli()
			}
		}
	} else {
		n.step = 0
	}

	n.timestamp = now

	return ((now - epoch) << timeShift) | (n.node << nodeShift) | n.step
}

// InitDefaultNode initializes the global default node (node ID 0 by default)
func InitDefaultNode(nodeID int64) {
	var err error
	defaultNode, err = NewNode(nodeID)
	if err != nil {
		panic(err)
	}
}

// ResolveSnowflakeNodeID returns a node ID that is stable and unique for a
// command replica. An explicit value takes precedence; otherwise a Kubernetes
// StatefulSet ordinal is extracted from the pod name.
func ResolveSnowflakeNodeID(explicit, podName string) (int64, error) {
	value := strings.TrimSpace(explicit)
	if value != "" {
		nodeID, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse SNOWFLAKE_NODE_ID: %w", err)
		}
		if _, err := NewNode(nodeID); err != nil {
			return 0, fmt.Errorf("validate SNOWFLAKE_NODE_ID: %w", err)
		}
		return nodeID, nil
	}

	name := strings.TrimSpace(podName)
	if name == "" {
		return 1, nil
	}
	separator := strings.LastIndexByte(name, '-')
	if separator < 0 || separator == len(name)-1 {
		return 0, fmt.Errorf("POD_NAME %q has no StatefulSet ordinal", name)
	}
	ordinal, err := strconv.ParseInt(name[separator+1:], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse StatefulSet ordinal from POD_NAME %q: %w", name, err)
	}
	nodeID := ordinal + 1
	if _, err := NewNode(nodeID); err != nil {
		return 0, fmt.Errorf("validate node ID derived from POD_NAME %q: %w", name, err)
	}
	return nodeID, nil
}

// GenerateSnowflake generates an int64 ID using the default node
func GenerateSnowflake() int64 {
	once.Do(func() {
		if defaultNode == nil {
			InitDefaultNode(1) // Default to node 1
		}
	})
	return defaultNode.Generate()
}

// GenerateUUIDv7 generates a new UUIDv7 string
func GenerateUUIDv7() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString() // Fallback to v4
}
