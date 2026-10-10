# Twitch GQL Hash Grabber

Este directorio contiene una herramienta sencilla y documentación para capturar de forma manual los hashes de consulta persistente (*Persisted Query Hashes*) oficiales de Twitch cuando sean rotados en el futuro.

## Archivos
* **[grabber.js](file:///c:/Users/Pablo/Desktop/GitHub/TDropFarmer/tools/hash_grabber/grabber.js)**: Script de JavaScript para pegar en la consola de tu navegador.

---

## Cómo usarlo (Paso a Paso)

1. Abre tu navegador (Chrome, Firefox, Edge, Brave, etc.) e ingresa a [Twitch.tv](https://www.twitch.tv). Asegúrate de iniciar sesión.
2. Abre las **Herramientas de Desarrollador** de tu navegador:
   * Puedes usar la tecla **`F12`** o las teclas **`Ctrl + Shift + I`**.
3. Ve a la pestaña **Console (Consola)**.
4. Abre el archivo **[grabber.js](file:///c:/Users/Pablo/Desktop/GitHub/TDropFarmer/tools/hash_grabber/grabber.js)**, copia todo su contenido y pégalo en la línea de comando de la consola. Presiona **Enter**.
   * *Nota: Deberías ver un mensaje en color verde indicando `[Twitch GQL] Hook activated successfully!`.*
5. A partir de este momento, navega por Twitch y realiza las acciones descritas en la siguiente sección para capturar cada hash.

> [!WARNING]
> Si recargas la página (F5) o navegas a un subdominio diferente de Twitch que recargue por completo la pestaña, tendrás que volver a pegar el script en la consola para reactivar el capturador.

---

## ¿Cómo hacer que aparezca cada Hash?

Para que aparezcan los hashes objetivo (que se imprimirán con una etiqueta llamativa `[BOT HASH FOUND]`), debes forzar a Twitch a realizar la acción asociada en el navegador:

### 1. Hashes de Puntos de Canal
* **`ChannelPointsContext`**:
  * **Acción**: Entra al canal en directo de cualquier streamer que tenga puntos de canal activos. El navegador lo consultará automáticamente al cargar el reproductor de vídeo y el chat.
* **`ClaimCommunityPoints`**:
  * **Acción**: Quédate viendo un stream y espera a que aparezca el cofre verde de puntos de canal (+50) en la parte inferior del chat. Haz clic en él para reclamarlo. La llamada se interceptará en ese instante.

### 2. Hashes de Drops e Inventario
* **`Inventory`**:
  * **Acción**: Ingresa a tu inventario de Drops de Twitch: `https://www.twitch.tv/drops/inventory`.
* **`ViewerDropsDashboard`**:
  * **Acción**: Ve a la página principal de campañas de Drops de Twitch: `https://www.twitch.tv/drops/campaigns` o recarga la página de inventario.
* **`DropCampaignDetails`**:
  * **Acción**: En la página de campañas de Drops, haz clic en alguna campaña específica para ver sus detalles (por ejemplo, para desplegar los detalles de los streamers y las horas de visualización requeridas).
* **`DropsPage_ClaimDropRewards`**:
  * **Acción**: En la página de tu inventario de Drops (`https://www.twitch.tv/drops/inventory`), haz clic en el botón de **Reclamar (Claim)** de cualquier Drop completado.

### 3. Hashes de Descubrimiento de Canales y Juegos (Discovery)
* **`DirectoryPage_Game`**:
  * **Acción**: Ingresa a la página de cualquier juego o categoría en Twitch (ej. `https://www.twitch.tv/directory/category/rust` o cualquier juego que tenga drops activos).
* **`DirectoryGameRedirect`**:
  * **Acción**: Navega desde el buscador general o la lista principal de directorios hacia la categoría de un juego.
* **`DropsHighlightService_AvailableDrops`**:
  * **Acción**: En la página de cualquier categoría o directorio de juego, activa o haz clic en el filtro de etiqueta **Drops activados** (*DropsEnabled*).

---

## ¿Dónde actualizar los Hashes en el Código Go?

Una vez capturados los hashes en la consola, debes actualizarlos en los siguientes archivos de tu bot:

### A. Para Puntos de Canal:
Archivo: **[internal/twitch/channelpoints/gql.go](file:///c:/Users/Pablo/Desktop/GitHub/TDropFarmer/internal/twitch/channelpoints/gql.go)**
* Modificar:
  * `channelPointsContextHash` (línea ~17)
  * `claimCommunityPointsHash` (línea ~14)

### B. Para Drops e Inventario:
Archivo: **[internal/twitch/inventory/inventory.go](file:///c:/Users/Pablo/Desktop/GitHub/TDropFarmer/internal/twitch/inventory/inventory.go)**
* Modificar:
  * `inventoryHash` (línea ~18)
  * `claimDropHash` (línea ~21)
  * `viewerCampaignsHash` (línea ~24)
  * `campaignDetailsHash` (línea ~27)

### C. Para Descubrimiento de Canales y Juegos:
Archivo: **[internal/twitch/discovery/discovery.go](file:///c:/Users/Pablo/Desktop/GitHub/TDropFarmer/internal/twitch/discovery/discovery.go)**
* Modificar:
  * `gameDirectoryHash` (línea ~17)
  * `gameRedirectHash` (línea ~20)
  * `availableDropsHash` (línea ~23)

