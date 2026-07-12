(() => {
	"use strict";

	const notes = {
		status: {
			file: "status.md",
			verified: "verified Jun 10",
			description: "Current state and next actions",
			kicker: "Current state",
			sources: "2 declared sources",
			title: "Claim status",
			summary: "Current state, supporting evidence, and the next action in one Markdown note.",
			content: `
				<dl class="hv2-note-meta">
					<div><dt>Claim</dt><dd>GM-2026-88341 · <span>Granite Mutual</span></dd></div>
					<div><dt>Event</dt><dd>Burst supply line · May 18, 2026</dd></div>
				</dl>
				<table class="hv2-note-metrics" aria-label="Claim estimate comparison">
					<thead><tr><th>Insurer estimate</th><th>Contractor quote</th><th>Gap to close</th></tr></thead>
					<tbody><tr><td>$11,200</td><td>$19,750</td><td>$8,550</td></tr></tbody>
				</table>
				<h4>Working position</h4>
				<p>Coverage is accepted. The unresolved issue is valuation: the policy is replacement-cost, while the carrier estimate depreciates the cabinets.</p>
				<h4>Next action</h4>
				<p><b>By Jun 20:</b> send the dispute to Dana Whitfield with the BlueOak Builders quote and policy §4.2.</p>`,
			backlinks: [
				["Damage assessment", "project · updated today"],
				["Drafted estimate dispute", "journal · updated today"],
			],
			history: [
				["Clarify response deadline", "8f2c1a · now"],
				["Add contractor quote", "41db70 · 2d ago"],
			],
			commit: ["8f2c1a", "clarify claim status and response deadline", "2 minutes ago"],
		},
		damage: {
			file: "damage-assessment.md",
			verified: "verified Jun 10",
			description: "Visual inspection record and repair implications",
			kicker: "Inspection record",
			sources: "2 declared sources",
			title: "Damage assessment",
			summary: "The failed supply line damaged the sink base, wall finish, and adjacent flooring.",
			content: `
				<dl class="hv2-inspection-figure" aria-label="Structured inspection record for the damaged cabinet base">
					<div><dt>Location</dt><dd>Kitchen sink base</dd></div>
					<div><dt>Observed</dt><dd>Jun 8 · 09:40</dd></div>
					<div><dt>Condition</dt><dd>Dry at inspection</dd></div>
				</dl>
				<table class="hv2-note-facts" aria-label="Damage assessment findings">
					<thead><tr><th>Area</th><th>Finding</th><th>Repair implication</th></tr></thead>
					<tbody><tr><td>Cabinet base</td><td>Swollen substrate</td><td>Replace</td></tr><tr><td>Wall finish</td><td>Opened for drying</td><td>Patch + paint</td></tr><tr><td>Flooring</td><td>1.2 m affected</td><td>Blend under review</td></tr></tbody>
				</table>
				<h4>Assessment</h4>
				<p>The visible damage supports a like-for-like repair scope. Retain the inspection image with the contractor quote and moisture log.</p>`,
			backlinks: [
				["Claim status", "project · current state"],
				["BlueOak Builders", "reference · repair scope"],
			],
			history: [
				["Add site inspection image", "b96d042 · now"],
				["Record moisture readings", "6a0131e · 2d ago"],
			],
			commit: ["b96d042", "add visual damage assessment", "just now"],
		},
		correspondence: {
			file: "correspondence-log.md",
			verified: "updated Jun 09",
			description: "Dated record of calls, emails, and promises",
			kicker: "Communication history",
			sources: "0 declared sources",
			title: "Correspondence log",
			summary: "Every contact, commitment, and follow-up in one chronological record.",
			content: `
				<div class="hv2-note-timeline">
					<div><time datetime="2026-06-09">Jun 09</time><p><strong>Public adjuster declined for now.</strong> The 10% contingency is not worth it unless the dispute stalls.</p></div>
					<div><time datetime="2026-06-08">Jun 08</time><p><strong>Contractor quote received.</strong> $19,750, valid for 60 days. Dana will compare scope after the written dispute arrives.</p></div>
					<div><time datetime="2026-06-04">Jun 04</time><p><strong>Carrier estimate received.</strong> $11,200 with a June 20 response deadline.</p></div>
				</div>`,
			backlinks: [
				["Claim status", "project · next action"],
				["Dana Whitfield", "reference · adjuster"],
			],
			history: [
				["Record public adjuster call", "4c7a2e · today"],
				["Record contractor quote", "0e1f9b4 · 1d ago"],
			],
			commit: ["4c7a2e", "record public adjuster call", "today"],
		},
	};

	const byId = (id) => document.getElementById(id);
	const buttons = Array.from(document.querySelectorAll("[data-hv2-note]"));
	const noteBody = byId("hv2-note-body");
	const notePanel = byId("hv2-note-panel");

	const renderContext = (root, items, history = false) => {
		if (!root) return;
		root.replaceChildren();
		items.forEach(([title, meta]) => {
			const row = document.createElement("div");
			if (history) {
				row.className = "hv2-history-item";
				const dot = document.createElement("i");
				dot.setAttribute("aria-hidden", "true");
				row.append(dot);
			}
			const text = document.createElement("span");
			const strong = document.createElement("strong");
			const small = document.createElement("small");
			strong.textContent = title;
			small.textContent = meta;
			text.append(strong, small);
			row.append(text);
			root.append(row);
		});
	};

	const setText = (id, value) => {
		const node = byId(id);
		if (node) node.textContent = value;
	};

	const renderNote = (key) => {
		const note = notes[key];
		if (!note) return;

		buttons.forEach((button) => {
			const active = button.dataset.hv2Note === key;
			button.classList.toggle("is-active", active);
			button.setAttribute("aria-selected", String(active));
			button.tabIndex = active ? 0 : -1;
			if (active && notePanel) notePanel.setAttribute("aria-labelledby", button.id);
		});

		setText("hv2-file-name", note.file);
		setText("hv2-verified", note.verified);
		setText("hv2-description", `description: ${note.description}`);
		setText("hv2-note-kicker", note.kicker);
		setText("hv2-note-sources", note.sources);
		setText("hv2-note-title", note.title);
		setText("hv2-note-summary", note.summary);
		setText("hv2-note-status", `Showing ${note.file}`);
		setText("hv2-commit-hash", note.commit[0]);
		setText("hv2-commit-message", note.commit[1]);
		setText("hv2-commit-time", note.commit[2]);

		const content = byId("hv2-note-content");
		if (content) content.innerHTML = note.content;
		renderContext(byId("hv2-backlinks"), note.backlinks);
		renderContext(byId("hv2-history"), note.history, true);

		if (noteBody) {
			noteBody.scrollTop = 0;
			noteBody.classList.remove("is-changing");
			void noteBody.offsetWidth;
			noteBody.classList.add("is-changing");
		}
	};

	buttons.forEach((button) => {
		button.addEventListener("click", () => renderNote(button.dataset.hv2Note));
		button.addEventListener("keydown", (event) => {
			if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
			event.preventDefault();
			const current = buttons.indexOf(button);
			let next = current;
			if (event.key === "Home") next = 0;
			else if (event.key === "End") next = buttons.length - 1;
			else if (event.key === "ArrowLeft" || event.key === "ArrowUp") next = (current - 1 + buttons.length) % buttons.length;
			else next = (current + 1) % buttons.length;
			buttons[next].focus();
			renderNote(buttons[next].dataset.hv2Note);
		});
	});
})();
