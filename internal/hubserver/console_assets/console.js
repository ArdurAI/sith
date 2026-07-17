(() => {
  "use strict";

  const workspace = document.querySelector('meta[name="sith-workspace"]')?.content || "";
  const csrfToken = document.querySelector('meta[name="sith-csrf"]')?.content || "";
  const correlationCSRFToken = document.querySelector('meta[name="sith-correlation-csrf"]')?.content || "";
  const inventoryCSRFToken = document.querySelector('meta[name="sith-inventory-csrf"]')?.content || "";
  const rail = document.getElementById("coverage-rail");
  const summary = document.getElementById("coverage-summary");
  const details = document.getElementById("coverage-details");
  const state = document.getElementById("console-state");
  const list = document.getElementById("cluster-list");
  const snapshotTime = document.getElementById("snapshot-time");
  const reload = document.getElementById("reload-fleet");
  const correlationForm = document.getElementById("correlation-form");
  const correlationKind = document.getElementById("correlation-kind");
  const correlationName = document.getElementById("correlation-name");
  const correlationNamespace = document.getElementById("correlation-namespace");
  const correlationButton = document.getElementById("run-correlation");
  const correlationState = document.getElementById("correlation-state");
  const correlationAnswer = document.getElementById("correlation-answer");
  const correlationSummary = document.getElementById("correlation-summary");
  const correlationTime = document.getElementById("correlation-time");
  const correlationGaps = document.getElementById("correlation-gaps");
  const correlationList = document.getElementById("correlation-list");
  const inventoryForm = document.getElementById("inventory-form");
  const inventoryKind = document.getElementById("inventory-kind");
  const inventoryNamespace = document.getElementById("inventory-namespace");
  const inventoryName = document.getElementById("inventory-name");
  const inventoryButton = document.getElementById("run-inventory");
  const inventoryState = document.getElementById("inventory-state");
  const inventoryAnswer = document.getElementById("inventory-answer");
  const inventorySummary = document.getElementById("inventory-summary");
  const inventoryTime = document.getElementById("inventory-time");
  const inventoryGaps = document.getElementById("inventory-gaps");
  const inventoryList = document.getElementById("inventory-list");

  const setState = (message, error = false) => {
    state.hidden = false;
    state.classList.toggle("error", error);
    state.replaceChildren(document.createElement("p"));
    state.firstElementChild.textContent = message;
  };

  const setCoverageDetailsMessage = (message) => {
    details.replaceChildren(document.createElement("li"));
    details.firstElementChild.textContent = message;
  };

  const clearCorrelation = () => {
    correlationAnswer.hidden = true;
    correlationSummary.textContent = "";
    correlationTime.textContent = "No correlation read yet";
    correlationGaps.replaceChildren();
    correlationList.replaceChildren();
  };

  const clearInventory = () => {
    inventoryAnswer.hidden = true;
    inventorySummary.textContent = "";
    inventoryTime.textContent = "No inventory read yet";
    inventoryGaps.replaceChildren();
    inventoryList.replaceChildren();
  };

  const setCorrelationState = (message, error = false, visuallyHidden = false) => {
    correlationState.hidden = false;
    correlationState.classList.toggle("error", error);
    correlationState.classList.toggle("visually-hidden", visuallyHidden);
    correlationState.replaceChildren(document.createElement("p"));
    correlationState.firstElementChild.textContent = message;
  };

  const setInventoryState = (message, error = false, visuallyHidden = false) => {
    inventoryState.hidden = false;
    inventoryState.classList.toggle("error", error);
    inventoryState.classList.toggle("visually-hidden", visuallyHidden);
    inventoryState.replaceChildren(document.createElement("p"));
    inventoryState.firstElementChild.textContent = message;
  };

  const setSessionExpired = () => {
    snapshotTime.textContent = "No valid snapshot";
    rail.replaceChildren();
    list.replaceChildren();
    setCoverageDetailsMessage("Named gaps are unavailable because the session expired.");
    summary.textContent = "The session expired. No current coverage claim can be made.";
    rail.setAttribute("aria-label", summary.textContent);
    state.hidden = false;
    state.classList.add("error");
    const message = document.createElement("p");
    message.append("The session expired. ");
    const login = document.createElement("a");
    login.className = "session-link";
    login.href = `/v1/workspaces/${encodeURIComponent(workspace)}/console/login`;
    login.textContent = "Sign in again";
    message.append(login, ".");
    state.replaceChildren(message);
    clearCorrelation();
    setCorrelationState("The session expired. Sign in again before running a correlation.", true);
    clearInventory();
    setInventoryState("The session expired. Sign in again before reading inventory.", true);
  };

  const setProofExpired = () => {
    snapshotTime.textContent = "No valid snapshot";
    rail.replaceChildren();
    list.replaceChildren();
    setCoverageDetailsMessage("Named gaps are unavailable until the authenticated console is reloaded.");
    summary.textContent = "The page proof expired or workspace access changed. No current coverage claim can be made.";
    rail.setAttribute("aria-label", summary.textContent);
    state.hidden = false;
    state.classList.add("error");
    const message = document.createElement("p");
    message.append("Reload the authenticated console before reading again. ");
    const reloadPage = document.createElement("a");
    reloadPage.className = "session-link";
    reloadPage.href = window.location.pathname;
    reloadPage.textContent = "Reload console";
    message.append(reloadPage, ".");
    state.replaceChildren(message);
  };

  const stringSet = (values) => new Set(Array.isArray(values) ? values.filter((value) => typeof value === "string") : []);

  const clusterState = (cluster, coverage) => {
    const name = typeof cluster?.name === "string" ? cluster.name : "";
    if (!cluster?.reachable || stringSet(coverage?.unreachable).has(name)) return "unreachable";
    if (stringSet(coverage?.stale).has(name)) return "stale";
    if (stringSet(coverage?.truncated).has(name)) return "truncated";
    return "current";
  };

  const appendRailSegment = (kind, label) => {
    const segment = document.createElement("span");
    segment.className = `rail-segment ${kind}`;
    segment.title = label;
    rail.append(segment);
  };

  const renderAssessmentDetails = (target, assessment, emptyMessage) => {
    target.replaceChildren();
    const appendDetail = (message) => {
      const item = document.createElement("li");
      item.textContent = message;
      target.append(item);
    };
    if (assessment?.inconsistent) appendDetail("Coverage metadata is internally inconsistent.");
    [
      ["Unreachable", assessment?.unreachable],
      ["Stale", assessment?.stale],
      ["Partial", assessment?.truncated],
    ].forEach(([label, values]) => {
      const names = [...stringSet(values)].sort((left, right) => left.localeCompare(right));
      if (names.length > 0) appendDetail(`${label}: ${names.join(", ")}`);
    });
    if (Number.isSafeInteger(assessment?.unaccounted) && assessment.unaccounted > 0) {
      appendDetail(`Unaccounted requested scopes: ${assessment.unaccounted}`);
    }
    if (target.childElementCount === 0) appendDetail(emptyMessage);
  };

  const renderCoverageDetails = (assessment) => renderAssessmentDetails(details, assessment, "No named coverage gaps.");

  const renderRail = (fleet, assessment) => {
    rail.replaceChildren();
    const coverage = fleet.coverage || {};
    const clusters = Array.isArray(fleet.clusters) ? fleet.clusters : [];
    const represented = new Set();
    clusters.forEach((cluster) => {
      const name = typeof cluster?.name === "string" && cluster.name ? cluster.name : "Unnamed cluster";
      represented.add(name);
      appendRailSegment(clusterState(cluster, coverage), name);
    });
    stringSet(coverage.unreachable).forEach((name) => {
      if (!represented.has(name)) appendRailSegment("unreachable", name);
    });
    const requested = Number.isSafeInteger(coverage.requested) && coverage.requested > 0 ? coverage.requested : 0;
    const missing = Math.max(0, requested - rail.childElementCount);
    for (let index = 0; index < missing; index += 1) appendRailSegment("unknown", "Unaccounted scope");
    if (rail.childElementCount === 0) appendRailSegment("unknown", "No requested scopes");

    const reachable = Number.isSafeInteger(coverage.reachable) ? coverage.reachable : 0;
    const gaps = Array.isArray(assessment?.gaps) ? assessment.gaps.join(", ") : "unknown";
    summary.textContent = requested === 0
      ? "No cluster scopes were requested. No fleet-health claim can be made."
      : assessment?.complete
      ? `${reachable} of ${requested} requested clusters are reachable with complete current coverage.`
      : `${reachable} of ${requested} requested clusters answered. Coverage is incomplete: ${gaps || "unclassified gap"}.`;
    rail.setAttribute("aria-label", summary.textContent);
    renderCoverageDetails(assessment);
  };

  const observedLabel = (value) => {
    if (typeof value !== "string" || !value) return "No observation time";
    const observed = new Date(value);
    if (Number.isNaN(observed.getTime())) return "Invalid observation time";
    return `Observed ${observed.toISOString()}`;
  };

  const renderClusters = (fleet) => {
    list.replaceChildren();
    const clusters = Array.isArray(fleet.clusters) ? [...fleet.clusters] : [];
    clusters.sort((left, right) => String(left?.name || "").localeCompare(String(right?.name || "")));
    if (clusters.length === 0) {
      setState("No cluster observations are available for this signed workspace. This is not a healthy-fleet claim.");
      return;
    }
    state.hidden = true;
    clusters.forEach((cluster, index) => {
      const status = clusterState(cluster, fleet.coverage || {});
      const row = document.createElement("li");
      row.className = "cluster-row";

      const sequence = document.createElement("span");
      sequence.className = "cluster-sequence";
      sequence.textContent = String(index + 1).padStart(2, "0");

      const name = document.createElement("h3");
      name.className = "cluster-name";
      name.textContent = typeof cluster?.name === "string" && cluster.name ? cluster.name : "Unnamed cluster";

      const source = document.createElement("p");
      source.className = "cluster-source";
      source.textContent = `Source ${typeof cluster?.source_kind === "string" && cluster.source_kind ? cluster.source_kind : "unknown"}`;

      const observed = document.createElement("p");
      observed.className = "cluster-observed";
      observed.textContent = observedLabel(cluster?.observed_at);

      const badge = document.createElement("span");
      badge.className = `state-badge ${status}`;
      badge.textContent = status;

      row.append(sequence, name, source, observed, badge);
      list.append(row);
    });
  };

  const setInventoryProofExpired = () => {
    clearInventory();
    inventoryState.hidden = false;
    inventoryState.classList.add("error");
    inventoryState.classList.remove("visually-hidden");
    const message = document.createElement("p");
    message.append("The inventory proof expired or workspace access changed. ");
    const reloadPage = document.createElement("a");
    reloadPage.className = "session-link";
    reloadPage.href = window.location.pathname;
    reloadPage.textContent = "Reload console";
    message.append(reloadPage, " before reading inventory again.");
    inventoryState.replaceChildren(message);
  };

  const appendInventoryMetric = (metrics, label, value) => {
    const item = document.createElement("div");
    const term = document.createElement("dt");
    const description = document.createElement("dd");
    term.textContent = label;
    description.textContent = value;
    item.append(term, description);
    metrics.append(item);
  };

  const renderInventory = (payload) => {
    const records = Array.isArray(payload.records) ? payload.records : [];
    const coverage = payload.coverage || {};
    const assessment = payload.assessment || {};
    inventoryList.replaceChildren();
    records.forEach((record, index) => {
      const row = document.createElement("li");
      row.className = "inventory-row";

      const sequence = document.createElement("span");
      sequence.className = "cluster-sequence";
      sequence.textContent = String(index + 1).padStart(2, "0");

      const identity = document.createElement("div");
      const resource = document.createElement("h3");
      resource.className = "cluster-name";
      resource.textContent = typeof record?.resource_kind === "string" ? record.resource_kind : "Resource";
      const namespace = typeof record?.namespace === "string" && record.namespace ? `${record.namespace}/` : "";
      const address = document.createElement("p");
      address.className = "resource-address";
      address.textContent = `${namespace}${typeof record?.name === "string" ? record.name : ""}`;
      const scope = document.createElement("p");
      scope.className = "cluster-source";
      scope.textContent = `Cluster ${typeof record?.scope === "string" ? record.scope : "unknown"}`;
      identity.append(resource, address, scope);

      const observed = document.createElement("p");
      observed.className = "cluster-observed";
      observed.textContent = observedLabel(record?.observed_at);

      const metrics = document.createElement("dl");
      metrics.className = "inventory-metrics";
      appendInventoryMetric(metrics, "Generation", Number.isSafeInteger(record?.generation) ? String(record.generation) : "Unavailable");
      if (Number.isSafeInteger(record?.replicas) && Number.isSafeInteger(record?.available_replicas)) {
        appendInventoryMetric(metrics, "Available", `${record.available_replicas} / ${record.replicas}`);
      }
      if (typeof record?.ready === "boolean") appendInventoryMetric(metrics, "Ready", record.ready ? "Yes" : "No");

      const freshness = document.createElement("p");
      freshness.className = `inventory-freshness${record?.stale ? " stale" : ""}`;
      freshness.textContent = record?.stale
        ? `Stale · ${typeof record?.stale_for === "string" ? record.stale_for : "age unavailable"}`
        : "Current";

      row.append(sequence, identity, observed, metrics, freshness);
      inventoryList.append(row);
    });

    const requested = Number.isSafeInteger(coverage.requested) ? coverage.requested : 0;
    const reachable = Number.isSafeInteger(coverage.reachable) ? coverage.reachable : 0;
    const gaps = Array.isArray(assessment.gaps) ? assessment.gaps.join(", ") : "unknown";
    if (requested === 0) {
      inventorySummary.textContent = "No cluster scopes were requested. No inventory-completeness claim can be made.";
    } else if (assessment.complete && records.length === 0) {
      inventorySummary.textContent = `No matching inventory record was observed across ${reachable} of ${requested} clusters with complete current coverage.`;
    } else if (assessment.complete) {
      inventorySummary.textContent = `${records.length} normalized inventory record${records.length === 1 ? "" : "s"} observed across complete current coverage (${reachable} of ${requested} clusters).`;
    } else {
      inventorySummary.textContent = `${records.length} normalized inventory record${records.length === 1 ? "" : "s"} observed; ${reachable} of ${requested} clusters answered. This is not a complete inventory: ${gaps || "unclassified gap"}.`;
    }
    renderAssessmentDetails(inventoryGaps, assessment, "No named inventory coverage gaps.");
    inventoryTime.textContent = `Read ${new Date().toISOString()}`;
    setInventoryState(`Inventory read complete. ${inventorySummary.textContent}`, false, true);
    inventoryAnswer.hidden = false;
  };

  const runInventory = async (event) => {
    event.preventDefault();
    clearInventory();
    if (!inventoryForm.checkValidity()) {
      inventoryForm.reportValidity();
      setInventoryState("Choose a supported resource kind. No inventory read was run.", true);
      return;
    }
    const kind = inventoryKind.value;
    const namespace = inventoryNamespace.value;
    const name = inventoryName.value;
    if (!["Deployment", "Pod", "Rollout"].includes(kind) || namespace.trim() !== namespace || name.trim() !== name) {
      setInventoryState("Use a supported kind and trimmed exact values. No inventory read was run.", true);
      return;
    }
    const parameters = new URLSearchParams();
    parameters.set("kind", kind);
    if (name) parameters.set("name", name);
    if (namespace) parameters.set("namespace", namespace);
    inventoryButton.disabled = true;
    setInventoryState("Reading one persisted, tenant-scoped inventory selection.");
    try {
      const response = await fetch(`${window.location.pathname}/inventory?${parameters.toString()}`, {
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        headers: { "X-Sith-CSRF": inventoryCSRFToken },
      });
      if (response.status === 401) {
        setSessionExpired();
        return;
      }
      if (response.status === 403) {
        setInventoryProofExpired();
        return;
      }
      if (!response.ok) throw new Error("inventory request refused");
      const payload = await response.json();
      if (!payload || typeof payload !== "object" || !Array.isArray(payload.records) || !payload.coverage || !payload.assessment) {
        throw new Error("invalid inventory response");
      }
      renderInventory(payload);
    } catch (_error) {
      clearInventory();
      setInventoryState("The persisted inventory could not be read. No connector refresh, local operation, or write was attempted.", true);
    } finally {
      inventoryButton.disabled = false;
    }
  };

  const setCorrelationProofExpired = () => {
    clearCorrelation();
    correlationState.hidden = false;
    correlationState.classList.add("error");
    correlationState.classList.remove("visually-hidden");
    const message = document.createElement("p");
    message.append("The correlation proof expired or workspace access changed. ");
    const reloadPage = document.createElement("a");
    reloadPage.className = "session-link";
    reloadPage.href = window.location.pathname;
    reloadPage.textContent = "Reload console";
    message.append(reloadPage, " before asking again.");
    correlationState.replaceChildren(message);
  };

  const renderCorrelation = (payload) => {
    const matches = Array.isArray(payload.matches) ? payload.matches : [];
    const coverage = payload.coverage || {};
    const assessment = payload.assessment || {};
    correlationList.replaceChildren();
    matches.forEach((match, index) => {
      const row = document.createElement("li");
      row.className = "correlation-row";

      const sequence = document.createElement("span");
      sequence.className = "cluster-sequence";
      sequence.textContent = String(index + 1).padStart(2, "0");

      const identity = document.createElement("div");
      const resource = document.createElement("h3");
      resource.className = "cluster-name";
      const namespace = typeof match?.namespace === "string" && match.namespace ? `${match.namespace}/` : "";
      resource.textContent = typeof match?.resource_kind === "string" ? match.resource_kind : "Resource";
      const address = document.createElement("p");
      address.className = "resource-address";
      address.textContent = `${namespace}${typeof match?.name === "string" ? match.name : ""}`;
      const scope = document.createElement("p");
      scope.className = "cluster-source";
      scope.textContent = `Cluster ${typeof match?.scope === "string" ? match.scope : "unknown"}`;
      identity.append(resource, address, scope);

      const observed = document.createElement("p");
      observed.className = "cluster-observed";
      observed.textContent = observedLabel(match?.observed_at);

      const health = document.createElement("span");
      const status = typeof match?.health === "string" ? match.health : "Unknown";
      health.className = `health-badge ${status.toLowerCase()}`;
      health.textContent = status;

      const freshness = document.createElement("p");
      freshness.className = "match-freshness";
      freshness.textContent = match?.stale
        ? `Stale · ${typeof match?.stale_for === "string" ? match.stale_for : "age unavailable"}`
        : "Current observation";

      row.append(sequence, identity, observed, health, freshness);
      correlationList.append(row);
    });

    const requested = Number.isSafeInteger(coverage.requested) ? coverage.requested : 0;
    const reachable = Number.isSafeInteger(coverage.reachable) ? coverage.reachable : 0;
    const gaps = Array.isArray(assessment.gaps) ? assessment.gaps.join(", ") : "unknown";
    if (requested === 0) {
      correlationSummary.textContent = "No cluster scopes were requested. No resource-health claim can be made.";
    } else if (assessment.complete && matches.length === 0) {
      correlationSummary.textContent = `No non-Healthy observation was found across ${reachable} of ${requested} clusters with complete current coverage.`;
    } else if (assessment.complete) {
      correlationSummary.textContent = `${matches.length} non-Healthy observation${matches.length === 1 ? "" : "s"} found across complete current coverage (${reachable} of ${requested} clusters).`;
    } else {
      correlationSummary.textContent = `${matches.length} non-Healthy observation${matches.length === 1 ? "" : "s"} found; ${reachable} of ${requested} clusters answered. This is not a complete answer: ${gaps || "unclassified gap"}.`;
    }
    renderAssessmentDetails(correlationGaps, assessment, "No named correlation coverage gaps.");
    correlationTime.textContent = `Read ${new Date().toISOString()}`;
    setCorrelationState(`Correlation complete. ${correlationSummary.textContent}`, false, true);
    correlationAnswer.hidden = false;
  };

  const runCorrelation = async (event) => {
    event.preventDefault();
    clearCorrelation();
    if (!correlationForm.checkValidity()) {
      correlationForm.reportValidity();
      setCorrelationState("Resource kind and exact name are required. No correlation was run.", true);
      return;
    }
    const kind = correlationKind.value;
    const name = correlationName.value;
    const namespace = correlationNamespace.value;
    if (kind.trim() !== kind || name.trim() !== name || namespace.trim() !== namespace || kind.toLowerCase() === "secret") {
      setCorrelationState("Use trimmed values and a non-Secret resource kind. No correlation was run.", true);
      return;
    }
    const parameters = new URLSearchParams();
    parameters.set("kind", kind);
    parameters.set("name", name);
    if (namespace) parameters.set("namespace", namespace);
    correlationButton.disabled = true;
    setCorrelationState("Reading one persisted, tenant-scoped health correlation.");
    try {
      const response = await fetch(`${window.location.pathname}/correlate?${parameters.toString()}`, {
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        headers: { "X-Sith-CSRF": correlationCSRFToken },
      });
      if (response.status === 401) {
        setSessionExpired();
        return;
      }
      if (response.status === 403) {
        setCorrelationProofExpired();
        return;
      }
      if (!response.ok) throw new Error("correlation request refused");
      const payload = await response.json();
      if (!payload || typeof payload !== "object" || !Array.isArray(payload.matches) || !payload.coverage || !payload.assessment) {
        throw new Error("invalid correlation response");
      }
      renderCorrelation(payload);
    } catch (_error) {
      clearCorrelation();
      setCorrelationState("The persisted correlation could not be read. No connector refresh, local operation, or write was attempted.", true);
    } finally {
      correlationButton.disabled = false;
    }
  };

  const readFleet = async () => {
    reload.disabled = true;
    setState("Reading the persisted workspace snapshot.");
    try {
      const response = await fetch(`${window.location.pathname}/fleet`, {
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        headers: { "X-Sith-CSRF": csrfToken },
      });
      if (response.status === 401) {
        setSessionExpired();
        return;
      }
      if (response.status === 403) {
        setProofExpired();
        return;
      }
      if (!response.ok) throw new Error("fleet request refused");
      const payload = await response.json();
      if (!payload || typeof payload !== "object" || !payload.fleet || !payload.assessment) throw new Error("invalid fleet response");
      renderRail(payload.fleet, payload.assessment);
      renderClusters(payload.fleet);
      snapshotTime.textContent = `Read ${new Date().toISOString()}`;
    } catch (_error) {
      snapshotTime.textContent = "No snapshot available";
      rail.replaceChildren();
      list.replaceChildren();
      setCoverageDetailsMessage("Named gaps are unavailable because the snapshot could not be read.");
      summary.textContent = "The fleet snapshot is unavailable. No coverage claim can be made.";
      rail.setAttribute("aria-label", summary.textContent);
      setState("The persisted fleet view could not be read. Try again; no connector refresh or write was attempted.", true);
    } finally {
      reload.disabled = false;
    }
  };

  reload.addEventListener("click", readFleet);
  inventoryForm.addEventListener("submit", runInventory);
  correlationForm.addEventListener("submit", runCorrelation);
  readFleet();
})();
