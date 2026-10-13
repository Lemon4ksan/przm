// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"
)

// RenderHTML generates a self-contained, interactive HTML/SVG report.
func RenderHTML(w io.Writer, r *AnalysisReport, filename string) error {
	jsonData, _ := json.MarshalIndent(r, "", "  ")

	var b strings.Builder

	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>PRZM Compression Diagnostics - ` + html.EscapeString(filename) + `</title>
<style>
  :root {
    --bg-primary: #0d1117;
    --bg-secondary: #161b22;
    --bg-tertiary: #21262d;
    --border-color: #30363d;
    --text-primary: #f0f6fc;
    --text-secondary: #8b949e;
    --accent-blue: #58a6ff;
    --accent-green: #3fb950;
    --accent-purple: #bc8cff;
    --accent-yellow: #d29922;
    --accent-red: #f85149;
    --accent-cyan: #39c5cf;
    --sec1-color: #3fb950;
    --sec2-color: #58a6ff;
    --sec3-color: #bc8cff;
    --sec4-color: #d29922;
    --font-mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace;
    --font-sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
  }

  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background-color: var(--bg-primary);
    color: var(--text-primary);
    font-family: var(--font-sans);
    line-height: 1.5;
    padding: 24px;
    max-width: 1300px;
    margin: 0 auto;
  }

  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    border-bottom: 1px solid var(--border-color);
    padding-bottom: 20px;
    margin-bottom: 24px;
    flex-wrap: wrap;
    gap: 16px;
  }

  .title-group h1 {
    font-size: 24px;
    font-weight: 700;
    display: flex;
    align-items: center;
    gap: 10px;
    color: #fff;
  }

  .badge {
    background: #1f6feb33;
    border: 1px solid #1f6feb66;
    color: var(--accent-blue);
    font-size: 12px;
    padding: 2px 8px;
    border-radius: 12px;
    font-family: var(--font-mono);
  }

  .subtitle {
    color: var(--text-secondary);
    font-size: 14px;
    margin-top: 4px;
    font-family: var(--font-mono);
  }

  .metrics-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: 16px;
    margin-bottom: 24px;
  }

  .card {
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 16px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.15);
  }

  .card-title {
    font-size: 12px;
    text-transform: uppercase;
    color: var(--text-secondary);
    letter-spacing: 0.5px;
    margin-bottom: 6px;
  }

  .card-value {
    font-size: 26px;
    font-weight: 700;
    font-family: var(--font-mono);
  }

  .card-subtext {
    font-size: 12px;
    color: var(--text-secondary);
    margin-top: 4px;
  }

  .highlight-ratio { color: var(--accent-green); }
  .highlight-blue { color: var(--accent-blue); }
  .highlight-purple { color: var(--accent-purple); }
  .highlight-yellow { color: var(--accent-yellow); }

  .section-layout {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 20px;
    margin-bottom: 24px;
  }

  @media (max-width: 900px) {
    .section-layout { grid-template-columns: 1fr; }
  }

  .panel-title {
    font-size: 16px;
    font-weight: 600;
    margin-bottom: 16px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    border-bottom: 1px solid var(--border-color);
    padding-bottom: 8px;
  }

  /* Table styling */
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
    font-family: var(--font-mono);
  }

  th, td {
    padding: 10px 12px;
    text-align: left;
    border-bottom: 1px solid var(--border-color);
  }

  th {
    color: var(--text-secondary);
    font-weight: 600;
    font-size: 11px;
    text-transform: uppercase;
  }

  tr:hover {
    background-color: var(--bg-tertiary);
  }

  .color-dot {
    display: inline-block;
    width: 10px;
    height: 10px;
    border-radius: 50%;
    margin-right: 8px;
  }

  /* Bars and SVG */
  .bar-container {
    height: 24px;
    display: flex;
    border-radius: 6px;
    overflow: hidden;
    margin: 16px 0;
    border: 1px solid var(--border-color);
  }

  .bar-seg {
    height: 100%;
    transition: width 0.3s ease;
  }

  .legend-list {
    display: flex;
    flex-wrap: wrap;
    gap: 16px;
    margin-top: 10px;
    font-size: 12px;
  }

  .legend-item {
    display: flex;
    align-items: center;
  }

  /* Histogram */
  .hist-bar-bg {
    background-color: var(--bg-tertiary);
    height: 16px;
    border-radius: 4px;
    overflow: hidden;
    position: relative;
    width: 100%;
  }

  .hist-bar-fill {
    height: 100%;
    border-radius: 4px;
  }

  /* Timeline */
  .timeline-grid {
    display: grid;
    grid-template-columns: repeat(32, 1fr);
    gap: 3px;
    margin: 16px 0;
  }

  .timeline-cell {
    height: 24px;
    border-radius: 2px;
    position: relative;
    cursor: pointer;
  }

  .timeline-cell:hover::after {
    content: attr(data-tooltip);
    position: absolute;
    bottom: 28px;
    left: 50%;
    transform: translateX(-50%);
    background: #000;
    border: 1px solid var(--border-color);
    padding: 4px 8px;
    border-radius: 4px;
    font-size: 11px;
    white-space: nowrap;
    z-index: 10;
    font-family: var(--font-mono);
  }

  .actions-btn {
    background: var(--bg-tertiary);
    border: 1px solid var(--border-color);
    color: var(--text-primary);
    padding: 6px 12px;
    border-radius: 6px;
    font-size: 12px;
    cursor: pointer;
    text-decoration: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    transition: background 0.2s;
  }
  .actions-btn:hover { background: #30363d; }

  pre {
    background: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    padding: 12px;
    overflow-x: auto;
    font-size: 12px;
    font-family: var(--font-mono);
    max-height: 400px;
  }
</style>
</head>
<body>
`)

	// Header
	fmt.Fprintf(&b, `
<header>
  <div class="title-group">
    <h1>PRZM Compressed Stream Inspector <span class="badge">%s</span></h1>
    <div class="subtitle">Target: %s &bull; Format: %s &bull; %d Frame(s)</div>
  </div>
  <div>
    <button class="actions-btn" onclick="toggleJSON()">View Raw JSON</button>
  </div>
</header>
`,
		func() string {
			if r.IsStream {
				return "PRZS STREAM"
			}
			return "PRZM FRAME"
		}(),
		html.EscapeString(filename),
		func() string {
			if r.IsStream {
				return "Multi-Chunk Streaming Container"
			}
			return "Single Frame (Independent)"
		}(),
		r.TotalFrames,
	)

	// Global Metrics Grid
	fmt.Fprintf(&b, `
<div class="metrics-grid">
  <div class="card">
    <div class="card-title">Compression Ratio</div>
    <div class="card-value highlight-ratio">%.2fx</div>
    <div class="card-subtext">%.2f%% space saved</div>
  </div>
  <div class="card">
    <div class="card-title">Bits Per Byte (BPC)</div>
    <div class="card-value highlight-blue">%.3f</div>
    <div class="card-subtext">Shannon efficiency metric</div>
  </div>
  <div class="card">
    <div class="card-title">Original Size</div>
    <div class="card-value">%s</div>
    <div class="card-subtext">%d bytes uncompressed</div>
  </div>
  <div class="card">
    <div class="card-title">Compressed Payload</div>
    <div class="card-value highlight-purple">%s</div>
    <div class="card-subtext">%d bytes packed</div>
  </div>
</div>
`,
		r.OverallRatio,
		r.OverallSavings,
		r.OverallBPC,
		formatBytes(int(r.TotalUSize)),
		r.TotalUSize,
		formatBytes(int(r.TotalCSize)),
		r.TotalCSize,
	)

	// Render each frame
	for i, frame := range r.Frames {
		frameHeader := ""
		if r.IsStream {
			frameHeader = fmt.Sprintf(
				`<h2 style="font-size:18px; margin: 24px 0 12px 0; color: var(--accent-cyan);">Chunk #%d (Uncompressed: %s, Compressed: %s, Ratio: %.2fx)</h2>`,
				i+1,
				formatBytes(frame.UncompressedSize),
				formatBytes(frame.CompressedSize),
				frame.CompressionRatio,
			)
		}
		b.WriteString(frameHeader)

		// Stacked Section Treemap Bar
		b.WriteString(`
<div class="card" style="margin-bottom: 24px;">
  <div class="panel-title">
    <span>4-Section Byte Distribution</span>
    <span style="font-size: 12px; font-weight: normal; color: var(--text-secondary);">De-interleaved Architecture</span>
  </div>
  <div class="bar-container">
`)
		colors := []string{"var(--sec1-color)", "var(--sec2-color)", "var(--sec3-color)", "var(--sec4-color)"}
		for _, s := range frame.Sections {
			if s.Percentage > 0 {
				fmt.Fprintf(
					&b,
					`<div class="bar-seg" style="width: %.2f%%; background-color: %s;" title="%s: %s (%.1f%%)"></div>`,
					s.Percentage,
					colors[s.Index-1],
					s.Name,
					formatBytes(s.PackedSize),
					s.Percentage,
				)
			}
		}
		b.WriteString(`</div>
  <div class="legend-list">
`)
		for _, s := range frame.Sections {
			fmt.Fprintf(
				&b,
				`<div class="legend-item"><span class="color-dot" style="background: %s;"></span><strong>%s:</strong>&nbsp;%s (%.1f%%) &bull; %s</div>`,
				colors[s.Index-1],
				s.Name,
				formatBytes(s.PackedSize),
				s.Percentage,
				s.ModeName,
			)
		}
		b.WriteString(`</div>
</div>
`)

		// Two-column layout: Section details & Offset locality
		b.WriteString(`
<div class="section-layout">
  <!-- Section Details Table -->
  <div class="card">
    <div class="panel-title">Section Architecture & Entropy</div>
    <table>
      <thead>
        <tr>
          <th>Section</th>
          <th>Size</th>
          <th>Share</th>
          <th>Entropy Mode</th>
          <th>Entropy</th>
        </tr>
      </thead>
      <tbody>
`)
		for _, s := range frame.Sections {
			fmt.Fprintf(&b, `
        <tr>
          <td><span class="color-dot" style="background: %s;"></span>%s</td>
          <td>%s</td>
          <td>%.1f%%</td>
          <td><span class="badge" style="font-size: 11px;">%s</span></td>
          <td>%.2f b/B</td>
        </tr>`,
				colors[s.Index-1], s.Name, formatBytes(s.PackedSize), s.Percentage, s.ModeName, s.EntropyBits)
		}
		b.WriteString(`
      </tbody>
    </table>
  </div>

  <!-- Locality & Distance Profile -->
  <div class="card">
    <div class="panel-title">Match Locality & Distance Spectrum</div>
    <table>
      <thead>
        <tr>
          <th>Metric / Range</th>
          <th>Count</th>
          <th>Share</th>
          <th style="width: 40%;">Distribution</th>
        </tr>
      </thead>
      <tbody>
`)
		// Repeat hits
		fmt.Fprintf(&b, `
        <tr>
          <td><strong style="color: var(--accent-green);">Repeat Matches (rep0..3)</strong></td>
          <td>%d</td>
          <td>%.1f%%</td>
          <td>
            <div class="hist-bar-bg"><div class="hist-bar-fill" style="width: %.1f%%; background: var(--accent-green);"></div></div>
          </td>
        </tr>
        <tr>
          <td><strong style="color: var(--accent-cyan);">Fused Brackets (d&le;32)</strong></td>
          <td>%d</td>
          <td>%.1f%%</td>
          <td>
            <div class="hist-bar-bg"><div class="hist-bar-fill" style="width: %.1f%%; background: var(--accent-cyan);"></div></div>
          </td>
        </tr>
        <tr>
          <td><strong style="color: var(--accent-purple);">Explicit Offsets</strong></td>
          <td>%d</td>
          <td>%.1f%%</td>
          <td>
            <div class="hist-bar-bg"><div class="hist-bar-fill" style="width: %.1f%%; background: var(--accent-purple);"></div></div>
          </td>
        </tr>`,
			frame.RepeatHits, frame.RepeatHitsPct, frame.RepeatHitsPct,
			frame.BracketHits, frame.BracketHitsPct, frame.BracketHitsPct,
			frame.ExplicitHits, frame.ExplicitHitsPct, frame.ExplicitHitsPct)

		// Buckets
		for _, bkt := range frame.OffsetBuckets {
			if bkt.Count > 0 {
				fmt.Fprintf(&b, `
        <tr>
          <td style="color: var(--text-secondary);">&nbsp;&bull; %s</td>
          <td>%d</td>
          <td>%.1f%%</td>
          <td>
            <div class="hist-bar-bg"><div class="hist-bar-fill" style="width: %.1f%%; background: var(--accent-blue);"></div></div>
          </td>
        </tr>`,
					bkt.Label, bkt.Count, bkt.Percentage, bkt.Percentage)
			}
		}
		b.WriteString(`
      </tbody>
    </table>
  </div>
</div>
`)

		// Opcode Families & Top Disassembly
		b.WriteString(`
<div class="section-layout">
  <!-- Opcode Families -->
  <div class="card">
    <div class="panel-title">Opcode Family Decomposition</div>
    <table>
      <thead>
        <tr>
          <th>Family Category</th>
          <th>Tokens</th>
          <th>Share</th>
          <th style="width: 35%;">Frequency</th>
        </tr>
      </thead>
      <tbody>
`)
		type famSort struct {
			fam OpcodeFamily
			cnt int
			pct float64
		}
		var fList []famSort
		for fam, cnt := range frame.FamilyCounts {
			if cnt > 0 {
				fList = append(fList, famSort{fam: fam, cnt: cnt, pct: frame.FamilyPct[fam]})
			}
		}
		// Sort by count desc
		for j := 0; j < len(fList)-1; j++ {
			for k := j + 1; k < len(fList); k++ {
				if fList[j].cnt < fList[k].cnt {
					fList[j], fList[k] = fList[k], fList[j]
				}
			}
		}

		for _, item := range fList {
			fmt.Fprintf(&b, `
        <tr>
          <td>%s</td>
          <td>%d</td>
          <td>%.1f%%</td>
          <td>
            <div class="hist-bar-bg"><div class="hist-bar-fill" style="width: %.1f%%; background: var(--accent-purple);"></div></div>
          </td>
        </tr>`,
				item.fam, item.cnt, item.pct, item.pct)
		}
		b.WriteString(`
      </tbody>
    </table>
  </div>

  <!-- Top Opcodes Disassembly -->
  <div class="card">
    <div class="panel-title">Top-10 Hot Opcodes (Section 2)</div>
    <table>
      <thead>
        <tr>
          <th>Opcode</th>
          <th>Format</th>
          <th>Semantics</th>
          <th>Count</th>
          <th>Share</th>
        </tr>
      </thead>
      <tbody>
`)
		for _, op := range frame.TopOpcodes {
			fmt.Fprintf(&b, `
        <tr>
          <td><span class="badge" style="font-size:11px; background:#30363d; border-color:#8b949e;">%s</span></td>
          <td>%s</td>
          <td>%s</td>
          <td>%d</td>
          <td>%.1f%%</td>
        </tr>`,
				op.Hex, op.Template, op.Name, op.Count, op.Percentage)
		}
		b.WriteString(`
      </tbody>
    </table>
  </div>
</div>
`)

		// File Timeline Density
		if len(frame.Timeline) > 0 {
			b.WriteString(`
<div class="card" style="margin-bottom: 24px;">
  <div class="panel-title">
    <span>Stream Reconstruction Timeline Heatmap</span>
    <span style="font-size: 12px; font-weight: normal; color: var(--text-secondary);">64 Quantiles &bull; Green = Repeat Matches, Blue = Long Matches, Purple = Literals</span>
  </div>
  <div class="timeline-grid">
`)
			for _, seg := range frame.Timeline {
				// Determine predominant color
				bg := "var(--sec1-color)"
				if seg.LiteralPct > 60 {
					bg = "var(--sec2-color)"
				} else if seg.RepeatPct > 50 {
					bg = "var(--accent-green)"
				} else if seg.MatchPct > 50 {
					bg = "var(--accent-purple)"
				}
				tooltip := fmt.Sprintf("[%d - %d]: Lit %.0f%% | Match %.0f%% | Rep %.0f%%",
					seg.Start, seg.End, seg.LiteralPct, seg.MatchPct, seg.RepeatPct)

				fmt.Fprintf(&b, `<div class="timeline-cell" style="background-color: %s;" data-tooltip="%s"></div>`,
					bg, html.EscapeString(tooltip))
			}
			b.WriteString(`
  </div>
</div>
`)
		}
	}

	// Hidden Raw JSON Drawer
	b.WriteString(`
<div id="jsonDrawer" style="display:none; margin-top: 24px;" class="card">
  <div class="panel-title">Full Diagnostic JSON Payload</div>
  <pre><code>` + html.EscapeString(string(jsonData)) + `</code></pre>
</div>

<script>
function toggleJSON() {
  var d = document.getElementById("jsonDrawer");
  if (d.style.display === "none") {
    d.style.display = "block";
    d.scrollIntoView({ behavior: 'smooth' });
  } else {
    d.style.display = "none";
  }
}
</script>

</body>
</html>
`)

	_, err := io.WriteString(w, b.String())
	return err
}
