package deployment

// Índices de las etapas del despliegue, en el orden en que ocurren.
const (
	StepValidate = iota
	StepCreateVM
	StepWaitBoot
	StepConfigure
	StepWaitFinalIP
	StepRegisterDNS
	StepPublish
	StepVerify
)

// Step describe una etapa del despliegue para mostrarla en la interfaz.
type Step struct {
	Key         string // identificador estable para el log y las métricas
	Label       string
	Details     []string
	Component   string // componente de la infraestructura que interviene
	FailMessage string // mensaje para el usuario si la etapa falla
}

// Steps son las etapas del despliegue; el índice coincide con las constantes Step*.
var Steps = []Step{
	StepValidate: {
		Key:         "validar",
		Label:       "Validando solicitud",
		Details:     []string{"Validando nombre del host", "Verificando archivo ZIP", "Reservando dirección IP"},
		Component:   "Aplicación de gestión",
		FailMessage: "No se pudo validar la solicitud.",
	},
	StepCreateVM: {
		Key:         "crear_vm",
		Label:       "Creando máquina virtual",
		Details:     []string{"Registrando la VM en VirtualBox", "Conectando el disco multiconexión", "Configurando red NAT y solo anfitrión"},
		Component:   "VirtualBox",
		FailMessage: "No se pudo crear la máquina virtual en VirtualBox.",
	},
	StepWaitBoot: {
		Key:         "esperar_arranque",
		Label:       "Esperando el arranque de la VM",
		Details:     []string{"Iniciando la VM sin interfaz gráfica", "Esperando SSH en la IP temporal"},
		Component:   "Máquina virtual",
		FailMessage: "La máquina virtual no respondió en la IP temporal.",
	},
	StepConfigure: {
		Key:         "configurar",
		Label:       "Configurando hostname e IP",
		Details:     []string{"Ejecutando configurar-instancia.sh", "Asignando hostname e IP definitivos", "Reiniciando la VM"},
		Component:   "Máquina virtual",
		FailMessage: "No se pudo configurar el hostname y la IP de la instancia.",
	},
	StepWaitFinalIP: {
		Key:         "esperar_ip",
		Label:       "Esperando la IP definitiva",
		Details:     []string{"Esperando el reinicio de la VM", "Esperando SSH en la IP definitiva"},
		Component:   "Red de la instancia",
		FailMessage: "La instancia no respondió en su IP definitiva.",
	},
	StepRegisterDNS: {
		Key:         "registrar_dns",
		Label:       "Registrando hostname en Bind9",
		Details:     []string{"Actualizando la zona con nsupdate", "Creando el registro A"},
		Component:   "Servidor DNS · Bind9",
		FailMessage: "No se pudo registrar el hostname en Bind9.",
	},
	StepPublish: {
		Key:         "publicar",
		Label:       "Publicando contenido web",
		Details:     []string{"Transfiriendo archivo ZIP", "Extrayendo archivos en /var/www/html"},
		Component:   "Servidor Apache",
		FailMessage: "No se pudo publicar el contenido en la instancia.",
	},
	StepVerify: {
		Key:         "verificar",
		Label:       "Verificando disponibilidad",
		Details:     []string{"Comprobando la respuesta HTTP de Apache"},
		Component:   "Servidor Apache",
		FailMessage: "La instancia no respondió por HTTP.",
	},
}
