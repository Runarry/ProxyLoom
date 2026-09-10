package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	labelMinLen  = 8
	labelStep    = 2
	labelHashLen = 64
	nodePrefix   = "n_"
	chainPrefix  = "c_"
)

type digestFunc func(material string) string

func sha256Hex(material string) string {
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:])
}

type labelItem struct {
	key      string
	material string
	prefix   string
	suffixes []string
}

type labeler struct {
	digest digestFunc
}

func defaultLabeler() labeler {
	return labeler{digest: sha256Hex}
}

func nodeMaterial(id ir.ID, revision int64) string {
	return "node|" + string(id) + "|" + strconv.FormatInt(revision, 10)
}

func chainMaterial(id ir.ID, revision int64) string {
	return "chain|" + string(id) + "|" + strconv.FormatInt(revision, 10)
}

func nodeLabelItem(id ir.ID, revision int64) labelItem {
	return labelItem{
		key:      nodeMaterial(id, revision),
		material: nodeMaterial(id, revision),
		prefix:   nodePrefix,
		suffixes: []string{""},
	}
}

func chainLabelItem(id ir.ID, revision int64) labelItem {
	return labelItem{
		key:      chainMaterial(id, revision),
		material: chainMaterial(id, revision),
		prefix:   chainPrefix,
		suffixes: []string{"_h1", "_h2"},
	}
}

func policyMaterial(id ir.ID, revision int64) string {
	return "policy|" + string(id) + "|" + strconv.FormatInt(revision, 10)
}
func policyLabelItem(id ir.ID, revision int64) labelItem {
	material := policyMaterial(id, revision)
	return labelItem{key: material, material: material, prefix: "g_", suffixes: []string{""}}
}

func (l labeler) assign(items []labelItem) (map[string][]string, error) {
	if l.digest == nil {
		l.digest = sha256Hex
	}
	type state struct {
		labelItem
		hex string
		n   int
	}
	states := make([]state, len(items))
	for i, item := range items {
		hexDigest := l.digest(item.material)
		if len(hexDigest) != labelHashLen {
			return nil, ir.Diagnostics{compileIssue(ir.CompileLabelCollision, "", "", "")}
		}
		states[i] = state{labelItem: item, hex: hexDigest, n: labelMinLen}
	}
	for {
		occupied := make(map[string]int, len(states)*2)
		collision := map[int]struct{}{}
		for i, st := range states {
			if st.n > labelHashLen {
				return nil, ir.Diagnostics{compileIssue(ir.CompileLabelCollision, "", "", "")}
			}
			stem := st.hex[:st.n]
			for _, suffix := range st.suffixes {
				tag := st.prefix + stem + suffix
				if j, ok := occupied[tag]; ok {
					collision[i] = struct{}{}
					collision[j] = struct{}{}
				}
				occupied[tag] = i
			}
		}
		if len(collision) == 0 {
			out := make(map[string][]string, len(states))
			for _, st := range states {
				tags := make([]string, len(st.suffixes))
				for i, suffix := range st.suffixes {
					tags[i] = st.prefix + st.hex[:st.n] + suffix
				}
				out[st.key] = tags
			}
			return out, nil
		}
		for i := range collision {
			states[i].n += labelStep
		}
	}
}

func compileIssue(code ir.DiagnosticCode, path, targetKey string, resource ir.ID) ir.Diagnostic {
	return ir.Diagnostic{
		Code:       code,
		Severity:   ir.SeverityError,
		FieldPath:  path,
		TargetKey:  targetKey,
		ResourceID: resource,
		Message:    code.Message(),
	}
}
