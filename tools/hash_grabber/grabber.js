// Twitch GQL Hash Sniffer
// Paste this code into your browser's Developer Console on Twitch.tv to intercept and display GQL hashes.

(function() {
    const targetOperations = [
        "ChannelPointsContext",
        "ClaimCommunityPoints",
        "Inventory",
        "ViewerDropsDashboard",
        "DropCampaignDetails",
        "DropsPage_ClaimDropRewards"
    ];

    const originalFetch = window.fetch;
    window.fetch = async function(...args) {
        const response = await originalFetch.apply(this, args);
        try {
            const url = args[0];
            if (typeof url === 'string' && url.includes('gql.twitch.tv')) {
                const options = args[1];
                if (options && options.body) {
                    const body = JSON.parse(options.body);

                    const processPayload = (item) => {
                        if (item && item.operationName && item.extensions?.persistedQuery?.sha256Hash) {
                            const isTarget = targetOperations.includes(item.operationName);
                            const labelStyle = isTarget 
                                ? 'color: #FFF; background: #9146FF; font-weight: bold; padding: 2px 5px; border-radius: 3px;' 
                                : 'color: #9146FF; font-weight: normal;';
                            const opStyle = 'color: #007ACC; font-weight: bold;';
                            const hashStyle = 'color: #4CAF50; font-weight: bold;';

                            if (isTarget) {
                                console.log(
                                    `%c[BOT HASH FOUND] %c${item.operationName}%c = "${item.extensions.persistedQuery.sha256Hash}"`, 
                                    labelStyle, 
                                    opStyle, 
                                    hashStyle
                                );
                            } else {
                                // Log other operations in a quieter style in case we need them later
                                console.log(
                                    `%c[Other GQL] %c${item.operationName}%c = "${item.extensions.persistedQuery.sha256Hash}"`, 
                                    'color: #888;', 
                                    'color: #555;', 
                                    'color: #666;'
                                );
                            }
                        }
                    };

                    if (Array.isArray(body)) {
                        body.forEach(processPayload);
                    } else {
                        processPayload(body);
                    }
                }
            }
        } catch (e) {
            // Ignore JSON parsing errors for non-JSON requests
        }
        return response;
    };
    console.log("%c[Twitch GQL Sniffer] Hook activated successfully! Active hashes will be printed here as you browse.", "color: #4CAF50; font-weight: bold; font-size: 1.1em;");
    console.log("Target operations monitored: " + targetOperations.join(", "));
})();
