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
    sqlResult: null, // último resultado do Console SQL { columns, rows }
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

  // ===================== MODAL DE DETALHES =====================
  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }

  // Aplica destaque de cores a um JSON já identado (string).
  function highlightJSON(jsonStr) {
    var esc = escapeHtml(jsonStr);
    return esc.replace(
      /("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false)\b|\bnull\b|-?\d+(?:\.\d+)?(?:[eE][+\-]?\d+)?)/g,
      function (match) {
        var cls = "json-number";
        if (/^"/.test(match)) {
          cls = /:$/.test(match) ? "json-key" : "json-string";
        } else if (/true|false/.test(match)) {
          cls = "json-boolean";
        } else if (/null/.test(match)) {
          cls = "json-null";
        }
        return '<span class="' + cls + '">' + match + "</span>";
      }
    );
  }

  // Tenta interpretar `str` como JSON; devolve {ok, pretty} com o JSON identado.
  function tryParseJSON(str) {
    if (!str) return { ok: false };
    var t = str.trim();
    if (t[0] !== "{" && t[0] !== "[") return { ok: false };
    try {
      return { ok: true, pretty: JSON.stringify(JSON.parse(t), null, 2) };
    } catch (e) {
      return { ok: false };
    }
  }

  // Monta um bloco "campo" com rótulo, conteúdo e (opcional) botão copiar.
  function fieldBlock(label, contentHtml, copyText) {
    var copyBtn = copyText != null
      ? '<button class="btn-copy" data-copy="' + escapeHtml(copyText).replace(/"/g, "&quot;") + '">copiar</button>'
      : "";
    return (
      '<div class="modal-field">' +
        '<div class="modal-field-head">' +
          '<span class="modal-field-label">' + label + "</span>" + copyBtn +
        "</div>" +
        contentHtml +
      "</div>"
    );
  }

  // Renderiza um valor (JSON identado ou texto cru) como bloco de código.
  function codeBlock(rawStr) {
    var parsed = tryParseJSON(rawStr);
    if (parsed.ok) {
      return {
        html: '<pre class="json-view">' + highlightJSON(parsed.pretty) + "</pre>",
        copy: parsed.pretty,
      };
    }
    return {
      html: '<pre class="json-view">' + escapeHtml(rawStr) + "</pre>",
      copy: rawStr,
    };
  }

  // Renderiza um valor genérico (de uma célula do SQL): JSON identado quando
  // aplicável, senão texto simples legível.
  function valueBlock(rawStr) {
    var parsed = tryParseJSON(rawStr);
    if (parsed.ok) {
      return {
        html: '<pre class="json-view">' + highlightJSON(parsed.pretty) + "</pre>",
        copy: parsed.pretty,
      };
    }
    if (rawStr === "" || rawStr == null) {
      return { html: '<div class="msg-text" style="color:var(--muted)">—</div>', copy: "" };
    }
    return { html: '<div class="msg-text">' + escapeHtml(rawStr) + "</div>", copy: rawStr };
  }

  function openModal(row) {
    if (!row) return;

    var lvl = $("modal-level");
    lvl.textContent = row.level || "—";
    lvl.className = "lvl lvl-" + (row.level || "");
    lvl.style.display = "";

    $("modal-time").textContent = fullTime(row);
    $("modal-id").textContent = "#" + row.id;

    var html = "";
    if (row.msg) {
      html += fieldBlock("mensagem", '<div class="msg-text">' + escapeHtml(row.msg) + "</div>", row.msg);
    }
    if (row.attrs) {
      var a = codeBlock(row.attrs);
      html += fieldBlock("atributos", a.html, a.copy);
    }
    if (row.raw) {
      var r = codeBlock(row.raw);
      html += fieldBlock("registro completo (raw)", r.html, r.copy);
    }
    $("modal-body").innerHTML = html;

    $("modal").classList.remove("hidden");
  }

  function closeModal() { $("modal").classList.add("hidden"); }

  // Abre o modal para uma linha do Console SQL (colunas genéricas).
  function openSqlModal(columns, values) {
    if (!columns || !values) return;

    var lower = columns.map(function (c) { return String(c).toLowerCase(); });
    var idxLevel = lower.indexOf("level");
    var idxId = lower.indexOf("id");
    var idxTime = lower.indexOf("time");
    if (idxTime < 0) idxTime = lower.indexOf("created_at");
    if (idxTime < 0) idxTime = lower.indexOf("ingested_at");

    var lvl = $("modal-level");
    if (idxLevel >= 0 && values[idxLevel]) {
      lvl.textContent = values[idxLevel];
      lvl.className = "lvl lvl-" + values[idxLevel];
      lvl.style.display = "";
    } else {
      lvl.style.display = "none";
    }

    $("modal-time").textContent = idxTime >= 0 ? (values[idxTime] || "") : "";
    $("modal-id").textContent = idxId >= 0 && values[idxId] ? "#" + values[idxId] : "";

    var html = "";
    for (var i = 0; i < columns.length; i++) {
      var v = valueBlock(values[i]);
      html += fieldBlock(escapeHtml(columns[i]), v.html, v.copy);
    }
    $("modal-body").innerHTML = html;

    $("modal").classList.remove("hidden");
  }

  // Timestamp completo e legível para o cabeçalho do modal.
  function fullTime(row) {
    var raw = row.time || row.ingested_at || "";
    if (!raw) return "";
    var d = new Date(raw);
    return isNaN(d.getTime()) ? raw : d.toLocaleString();
  }

  // Clique em uma linha abre o modal (delegação de evento).
  $("logs-body").addEventListener("click", function (e) {
    var tr = e.target.closest("tr.clickable");
    if (!tr) return;
    var id = parseInt(tr.getAttribute("data-id"), 10);
    var row = state.logs.find(function (l) { return l.id === id; });
    openModal(row);
  });

  // Clique em uma linha do resultado SQL abre o modal.
  $("sql-output").addEventListener("click", function (e) {
    var tr = e.target.closest("tr.clickable");
    if (!tr || !state.sqlResult) return;
    var ri = parseInt(tr.getAttribute("data-ri"), 10);
    var values = state.sqlResult.rows[ri];
    if (values) openSqlModal(state.sqlResult.columns, values);
  });

  $("modal-close").addEventListener("click", closeModal);
  $("modal").addEventListener("click", function (e) {
    if (e.target === $("modal")) closeModal();
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && !$("modal").classList.contains("hidden")) closeModal();
  });
  // Botões "copiar" dentro do modal.
  $("modal-body").addEventListener("click", function (e) {
    var btn = e.target.closest(".btn-copy");
    if (!btn) return;
    var text = btn.getAttribute("data-copy");
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text).then(function () {
        var prev = btn.textContent;
        btn.textContent = "copiado!";
        setTimeout(function () { btn.textContent = prev; }, 1200);
      });
    }
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
        state.sqlResult = { columns: res.d.columns || [], rows: res.d.rows || [] };
        out.innerHTML = tplResult({
          columns: state.sqlResult.columns,
          rows: state.sqlResult.rows,
          rowCount: state.sqlResult.rows.length,
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
