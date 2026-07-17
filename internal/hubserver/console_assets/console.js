(() => {
  "use strict";

  const workspace = document.querySelector('meta[name="sith-workspace"]')?.content || "";
  const csrfToken = document.querySelector('meta[name="sith-csrf"]')?.content || "";
  const rail = document.getElementById("coverage-rail");
  const summary = document.getElementById("coverage-summary");
  const details = document.getElementById("coverage-details");
  const state = document.getElementById("console-state");
  const list = document.getElementById("cluster-list");
  const snapshotTime = document.getElementById("snapshot-time");
  const reload = document.getElementById("reload-fleet");

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

  const renderCoverageDetails = (assessment) => {
    details.replaceChildren();
    const appendDetail = (message) => {
      const item = document.createElement("li");
      item.textContent = message;
      details.append(item);
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
    if (details.childElementCount === 0) appendDetail("No named coverage gaps.");
  };

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
  readFleet();
})();
