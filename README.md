# httpaas-go

Servicio administrado de publicación temporal de páginas y aplicaciones web (HTTPaaS)
sobre infraestructura virtualizada con Oracle VirtualBox, Apache HTTP Server y Bind9.

Aplicación web de gestión desarrollada en Go — Computación en la Nube 2026-2, Universidad del Quindío.

## Requisitos

- Go 1.22 o superior
- Oracle VirtualBox con `VBoxManage` en el PATH
- Cliente OpenSSH (`ssh`, `scp`) en el anfitrión
- VM `ns` con Bind9 (192.168.10.10) y plantilla `ApacheServer.vdi` en modo multiconexión

## Ejecución

    go run .

La aplicación queda disponible en http://localhost:8080