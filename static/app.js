"use strict";

(() => {
  const toast = document.querySelector("[data-toast]");
  let toastTimer;

  // Aviso flotante (copiar URL, instancia creada o eliminada).
  function showToast(message) {
    if (!toast) return;
    toast.querySelector("[data-toast-text]").textContent = message;
    toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { toast.hidden = true; }, 2600);
  }

  // Si el servidor ya trae un aviso (después de una redirección), se oculta solo.
  if (toast && !toast.hidden) {
    toastTimer = setTimeout(() => { toast.hidden = true; }, 2600);
  }

  // Menú lateral en pantallas pequeñas.
  const sidebar = document.querySelector("[data-sidebar]");
  const scrim = document.querySelector("[data-scrim]");

  function setMenu(open) {
    if (!sidebar) return;
    sidebar.classList.toggle("sidebar-open", open);
    if (scrim) scrim.hidden = !open;
  }

  document.querySelector("[data-menu-open]")?.addEventListener("click", () => setMenu(true));
  document.querySelector("[data-menu-close]")?.addEventListener("click", () => setMenu(false));
  scrim?.addEventListener("click", () => setMenu(false));

  // Copiar URL al portapapeles.
  document.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-copy]");
    if (!button) return;

    const value = button.dataset.copy;
    try {
      await navigator.clipboard.writeText(value);
      showToast("URL copiada al portapapeles");
    } catch {
      showToast(value);
    }
  });

  // Modal de confirmación para eliminar una instancia.
  const deleteDialog = document.querySelector("[data-delete-dialog]");
  if (deleteDialog) {
    const form = deleteDialog.querySelector("form");
    const submit = form.querySelector("[type=submit]");
    const submitLabel = submit.querySelector("[data-label]");

    document.addEventListener("click", (event) => {
      const button = event.target.closest("[data-delete]");
      if (!button) return;

      const data = button.dataset;
      deleteDialog.querySelectorAll("[data-field]").forEach((element) => {
        element.textContent = data[element.dataset.field] ?? "";
      });
      form.action = `/instances/${encodeURIComponent(data.hostname)}/delete`;
      submit.disabled = false;
      submitLabel.textContent = "Eliminar instancia";
      deleteDialog.showModal();
    });

    deleteDialog.querySelector("[data-dialog-cancel]").addEventListener("click", () => deleteDialog.close());
    deleteDialog.addEventListener("click", (event) => {
      if (event.target === deleteDialog) deleteDialog.close();
    });
    form.addEventListener("submit", () => {
      submit.disabled = true;
      submitLabel.textContent = "Eliminando…";
    });
  }

  // Buscador por hostname y filtro por estado.
  const search = document.querySelector("[data-search]");
  if (search) {
    const statusFilter = document.querySelector("[data-status-filter]");
    const items = document.querySelectorAll("[data-instance]");
    const empty = document.querySelector("[data-search-empty]");
    const results = document.querySelector("[data-search-results]");
    const clearButtons = document.querySelectorAll("[data-search-clear]");

    function applyFilters() {
      const query = search.value.trim().toLowerCase();
      const status = statusFilter ? statusFilter.value : "";
      let visible = 0;

      items.forEach((item) => {
        const match = item.dataset.instance.includes(query)
          && (status === "" || item.dataset.status === status);
        item.hidden = !match;
        if (match) visible++;
      });

      if (empty) empty.hidden = visible > 0;
      if (results) results.hidden = visible === 0;
      clearButtons.forEach((button) => {
        if (button.closest(".search-box")) button.hidden = query === "";
      });
    }

    search.addEventListener("input", applyFilters);
    statusFilter?.addEventListener("change", applyFilters);
    clearButtons.forEach((button) => button.addEventListener("click", () => {
      search.value = "";
      if (statusFilter) statusFilter.value = "";
      applyFilters();
      search.focus();
    }));
  }

  // Formulario de despliegue.
  const deployForm = document.querySelector("[data-deploy-form]");
  if (deployForm) {
    const domain = deployForm.dataset.domain;
    const maxSize = Number(deployForm.dataset.maxSize);
    const hostInput = deployForm.querySelector("[name=hostname]");
    const hostBox = deployForm.querySelector(".hostname-input");
    const fileInput = deployForm.querySelector("[name=content]");
    const dropzone = deployForm.querySelector("[data-dropzone]");
    const selectedFile = deployForm.querySelector("[data-selected-file]");
    const errorBox = deployForm.querySelector("[data-form-error]");
    const notice = deployForm.querySelector("[data-form-notice]");
    const submit = deployForm.querySelector("[type=submit]");
    const hostnamePattern = /^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$/;

    const setText = (selector, value) => {
      deployForm.querySelectorAll(selector).forEach((element) => { element.textContent = value; });
    };

    function showError(message) {
      errorBox.querySelector("[data-error-text]").textContent = message;
      errorBox.hidden = message === "";
    }

    function formatSize(bytes) {
      if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
      return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    }

    function updateHostname() {
      const value = hostInput.value.toLowerCase().replace(/\s+/g, "-");
      if (value !== hostInput.value) hostInput.value = value;
      hostBox.classList.remove("input-error");
      setText("[data-preview-url]", `http://${value || "mi-aplicacion"}.${domain}`);
      setText("[data-summary-domain]", `${value || "Sin definir"}.${domain}`);
    }

    function updateFile() {
      const file = fileInput.files[0];
      dropzone.hidden = Boolean(file);
      selectedFile.hidden = !file;
      setText("[data-summary-file]", file ? file.name : "Sin seleccionar (solo crear)");
      setText("[data-submit-label]", file ? "Desplegar aplicación" : "Crear instancia");
      if (file) {
        setText("[data-file-name]", file.name);
        setText("[data-file-size]", `${formatSize(file.size)} · Archivo ZIP`);
      }
    }

    function fileError(file) {
      if (!file.name.toLowerCase().endsWith(".zip")) return "El archivo debe estar en formato ZIP.";
      if (file.size > maxSize) return "El archivo supera el tamaño máximo de 50 MB.";
      return "";
    }

    function acceptFile(file) {
      if (!file) return;
      const message = fileError(file);
      if (message) {
        fileInput.value = "";
        showError(message);
      } else {
        // Al arrastrar, el archivo se asigna al input para que viaje con el formulario.
        if (fileInput.files[0] !== file) {
          const transfer = new DataTransfer();
          transfer.items.add(file);
          fileInput.files = transfer.files;
        }
        showError("");
      }
      updateFile();
    }

    hostInput.addEventListener("input", () => { updateHostname(); showError(""); });
    fileInput.addEventListener("change", () => acceptFile(fileInput.files[0]));
    deployForm.querySelector("[data-remove-file]").addEventListener("click", () => {
      fileInput.value = "";
      updateFile();
    });

    ["dragenter", "dragover"].forEach((type) => dropzone.addEventListener(type, (event) => {
      event.preventDefault();
      dropzone.classList.add("dragging");
    }));
    ["dragleave", "drop"].forEach((type) => dropzone.addEventListener(type, (event) => {
      event.preventDefault();
      dropzone.classList.remove("dragging");
    }));
    dropzone.addEventListener("drop", (event) => acceptFile(event.dataTransfer.files[0]));

    deployForm.addEventListener("submit", (event) => {
      const hostname = hostInput.value.trim();
      let message = "";
      if (hostname === "") message = "Ingresa un nombre para continuar.";
      else if (!hostnamePattern.test(hostname)) message = "Usa solo letras minúsculas, números y guiones; empieza por una letra y no termines en guion.";

      if (message) {
        event.preventDefault();
        hostBox.classList.add("input-error");
        showError(message);
        hostInput.focus();
        return;
      }

      submit.disabled = true;
      notice.hidden = false;
    });

    updateHostname();
    updateFile();
  }

  // Progreso de un despliegue en curso.
  const progress = document.querySelector("[data-progress]");
  if (progress) {
    const apiURL = progress.dataset.api;
    const publishStep = Number(progress.dataset.publishStep);
    const stepLabels = { complete: "Completado", current: "En proceso...", pending: "Pendiente", skipped: "Omitido" };

    function renderProgress(snap) {
      progress.querySelector("[data-percent]").textContent = `${snap.percent}%`;
      progress.querySelector("[data-bar]").style.width = `${snap.percent}%`;

      progress.querySelectorAll("[data-step]").forEach((element) => {
        const index = Number(element.dataset.step);
        const state = index < snap.step ? "complete" : index === snap.step ? "current" : "pending";
        const skipped = index === publishStep && !snap.hasContent && state === "complete";
        element.className = `step ${state}${skipped ? " skipped" : ""}`;
        element.querySelector("small").textContent = skipped ? stepLabels.skipped : stepLabels[state];
      });

      progress.querySelectorAll("[data-infra]").forEach((item) => {
        let ready = snap.step > Number(item.dataset.readyAfter);
        let value = ready ? item.dataset.ready : item.dataset.pending;

        if (item.dataset.infra === "ip") {
          ready = snap.ip !== "";
          value = ready ? snap.ip : item.dataset.pending;
        }
        if (item.dataset.infra === "content" && !snap.hasContent) {
          ready = true;
          value = item.dataset.ready;
        }

        item.classList.toggle("ready", ready);
        item.querySelector("strong").classList.toggle("pending-value", !ready);
        item.querySelector("[data-value]").textContent = value;
      });
    }

    async function pollProgress() {
      try {
        const response = await fetch(apiURL, { headers: { Accept: "application/json" }, cache: "no-store" });
        if (response.ok) {
          const snap = await response.json();
          if (snap.status !== "running") {
            // El servidor genera la pantalla final de éxito o de error.
            window.location.reload();
            return;
          }
          renderProgress(snap);
        }
      } catch {
        // Si falla una consulta (por ejemplo, la app se está reiniciando), se intenta en la siguiente vuelta.
      }
      setTimeout(pollProgress, 2000);
    }

    pollProgress();
  }
})();