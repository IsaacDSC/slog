/* app.js — lógica do visualizador de logs do slog.
 * Renderiza com Handlebars, faz polling incremental nos endpoints JSON e
 * mantém o estado do tema (light/dark) e dos filtros. */

(function () {
  "use strict";

  // ---- Handlebars: helper de formatação de horário ----
  Handlebars.registerHelper("displayTime", function (row) {
    var raw = row.time || row.ingested_at || "";
    if (!raw) return "—";
    var d = new Date(raw);
    if (isNaN(d.getTime())) return raw;
    var pad = function (n) { return (n < 10 ? "0" : "") + n; };
    return (
      pad(d.getHours()) + ":" + pad(d.getMinutes()) + ":" + pad(d.getSeconds()) +
      "." + String(d.getMilliseconds()).padStart(3, "0")
    );
  });

  var tplLogs = Handlebars.compile(document.getElementById("tpl-logs").innerHTML);
  var tplResult = Handlebars.compile(document.getElementById("tpl-result").innerHTML);

  var $ = function (id) { return document.getElementById(id); };

  // ---- Estado ----
  var state = {
    logs: [],        // ordenados do mais novo para o mais antigo
    maxId: 0,
    limit: 200,
    live: true,
    paused: false,
    timer: null,
  };

  // ===================== TEMA =====================
  var THEME_KEY = "slog-theme";
  function applyTheme(t) {
    document.documentElement.setAttribute("data-theme", t);
    $("btn-theme").textContent = (t === "dark" ? "☀️ Claro" : "🌙 Escuro");
    localStorage.setItem(THEME_KEY, t);
  }
  $("btn-theme").addEventListener("click", function () {
    var cur = document.documentElement.getAttribute("data-theme");
    applyTheme(cur === "dark" ? "light" : "dark");
  });
  applyTheme(localStorage.getItem(THEME_KEY) || "dark");

  // ===================== ABAS =====================
  document.querySelectorAll(".tabs button").forEach(function (btn) {
    btn.addEventListener("click", function () {
      document.querySelectorAll(".tabs button").forEach(function (b) { b.classList.remove("active"); });
      btn.classList.add("active");
      var tab = btn.dataset.tab;
      $("tab-logs").classList.toggle("hidden", tab !== "logs");
      $("tab-sql").classList.toggle("hidden", tab !== "sql");
    });
  });

  // ===================== LOGS =====================
  function filterParams(sinceId) {
    var p = new URLSearchParams();
    var text = $("f-text").value.trim();
    var level = $("f-level").value;
    if (text) p.set("q", text);
    if (level) p.set("level", level);
    p.set("limit", String(state.limit));
    if (sinceId) p.set("since_id", String(sinceId));
    return p.toString();
  }

  function renderLogs(emptyMsg) {
    var body = $("logs-body");
    body.innerHTML = tplLogs({ rows: state.logs });
    var empty = $("logs-empty");
    empty.textContent = emptyMsg || "sem logs ainda…";
    empty.classList.toggle("hidden", state.logs.length > 0);
    $("stat-total").textContent = state.logs.length;
  }

  // Carga inicial (ou após mudar filtro): pega as mais recentes.
  function loadInitial() {
    state.logs = [];
    state.maxId = 0;
    fetch("/api/logs?" + filterParams(0))
      .then(function (r) { return r.json(); })
      .then(function (data) {
        if (data.error) {
          state.logs = [];
          renderLogs("erro: " + data.error);
          setPollStatus("erro");
          return;
        }
        state.logs = data.logs || [];          // já vêm em ordem decrescente
        state.maxId = data.maxId || 0;
        renderLogs(state.logs.length ? "" : "nenhum log corresponde ao filtro");
        setPollStatus(state.paused ? "pausado" : (state.live ? "ao vivo" : "polling"));
      })
      .catch(function () {
        // Falha de rede (servidor encerrou?): limpa a tabela para não exibir
        // dados velhos que contradizem o filtro atual.
        state.logs = [];
        renderLogs("sem conexão com o servidor — o processo slog foi encerrado?");
        setPollStatus("offline");
      });
  }

  // Polling incremental: só busca id > maxId e prepende.
  function poll() {
    if (state.paused) return;
    if (!state.live) { return; } // sem modo ao vivo, não busca incremental
    fetch("/api/logs?" + filterParams(state.maxId))
      .then(function (r) { return r.json(); })
      .then(function (data) {
        if (data.error) return;
        var nw = data.logs || [];
        if (nw.length) {
          // nw vem em ordem crescente; inverte para o mais novo ficar no topo.
          nw.reverse();
          state.logs = nw.concat(state.logs).slice(0, state.limit);
          state.maxId = Math.max(state.maxId, data.maxId || 0);
          renderLogs();
        }
        setPollStatus("ao vivo");
      })
      .catch(function () {
        state.logs = [];
        renderLogs("sem conexão com o servidor — o processo slog foi encerrado?");
        setPollStatus("offline");
      });
  }

  function setPollStatus(s) { $("stat-poll").textContent = s; }

  function startTimer() {
    if (state.timer) clearInterval(state.timer);
    state.timer = setInterval(poll, 2000);
  }

  // Filtros
  var debounce;
  $("f-text").addEventListener("input", function () {
    clearTimeout(debounce);
    debounce = setTimeout(loadInitial, 300);
  });
  $("f-level").addEventListener("change", loadInitial);
  $("f-limit").addEventListener("change", function () {
    state.limit = parseInt($("f-limit").value, 10) || 200;
    loadInitial();
  });
  $("f-live").addEventListener("change", function () {
    state.live = $("f-live").checked;
    setPollStatus(state.live ? "ao vivo" : "polling pausado");
  });
  $("btn-clear").addEventListener("click", function () {
    $("f-text").value = "";
    $("f-level").value = "";
    loadInitial();
  });
  $("btn-pause").addEventListener("click", function () {
    state.paused = !state.paused;
    $("btn-pause").textContent = state.paused ? "▶ Retomar" : "⏸ Pausar";
    setPollStatus(state.paused ? "pausado" : "ao vivo");
    if (!state.paused) poll();
  });

  // Popula os níveis disponíveis.
  function loadLevels() {
    fetch("/api/levels")
      .then(function (r) { return r.json(); })
      .then(function (data) {
        var sel = $("f-level");
        var cur = sel.value;
        sel.innerHTML = '<option value="">todos os níveis</option>';
        (data.levels || []).forEach(function (l) {
          var o = document.createElement("option");
          o.value = l; o.textContent = l;
          sel.appendChild(o);
        });
        sel.value = cur;
      })
      .catch(function () {});
  }

  // ===================== CONSOLE SQL =====================
  function runQuery() {
    var sql = $("sql-input").value;
    var out = $("sql-output");
    out.innerHTML = '<div class="result-meta">executando…</div>';
    fetch("/api/query", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sql: sql }),
    })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        if (!res.ok || res.d.error) {
          out.innerHTML = '<div class="error"></div>';
          out.querySelector(".error").textContent = "Erro: " + (res.d.error || "consulta falhou");
          return;
        }
        out.innerHTML = tplResult({
          columns: res.d.columns || [],
          rows: res.d.rows || [],
          rowCount: (res.d.rows || []).length,
        });
      })
      .catch(function (e) {
        out.innerHTML = '<div class="error"></div>';
        out.querySelector(".error").textContent = "Erro de rede: " + e.message;
      });
  }
  $("btn-run").addEventListener("click", runQuery);
  $("sql-input").addEventListener("keydown", function (e) {
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") { e.preventDefault(); runQuery(); }
  });
  document.querySelectorAll("#sql-examples code").forEach(function (c) {
    c.addEventListener("click", function () {
      $("sql-input").value = c.textContent;
      runQuery();
    });
  });

  // ===================== INÍCIO =====================
  loadLevels();
  loadInitial();
  startTimer();
  setInterval(loadLevels, 10000); // atualiza a lista de níveis periodicamente
})();
