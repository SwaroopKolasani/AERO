<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style>
        body {
            background-color: #000000;
            color: #ffffff;
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            display: flex;
            flex-direction: column;
            align-items: center;
            justify-content: center;
            padding: 4rem 2rem;
            margin: 0;
            text-align: center;
        }

        .logo-container {
            margin-bottom: 2rem;
        }

        .logo-container img {
            max-width: 300px;
            height: auto;
            /* High contrast red filter if needed to match brand exactly */
            filter: drop-shadow(0 0 15px rgba(155, 15, 6, 0.2));
        }

        h1 {
            font-size: 3.5rem;
            letter-spacing: 0.5rem;
            font-weight: 700;
            color: #ffffff;
            margin: 0;
            text-transform: uppercase;
        }

        .tagline {
            font-size: 1.2rem;
            color: #9B0F06;
            font-weight: 500;
            margin-top: 1rem;
            max-width: 600px;
            line-height: 1.6;
            letter-spacing: 0.05rem;
        }

        .description {
            margin-top: 3rem;
            max-width: 700px;
            color: #a0a0a0;
            line-height: 1.8;
            font-size: 1rem;
        }

        .divider {
            width: 50px;
            height: 2px;
            background-color: #9B0F06;
            margin: 2rem 0;
        }
    </style>
    <title>AERO | Documentation</title>
</head>
<body>

    <div class="logo-container">
        <img src="https://github.com/SwaroopKolasani/AERO/blob/main/asserts/logo.png" alt="AERO Logo">
    </div>

    <h1>AERO</h1>

    <div class="divider"></div>

    <p class="tagline">
        Multi-cloud ephemeral compute bursting <br> 
        with content-addressed memoization.
    </p>

    <div class="description">
        <p>
            AERO is designed for low-latency synchronization between local environments and global cloud infrastructure. By leveraging content-addressed memoization, AERO eliminates redundant execution across network nodes, ensuring that computed results are instantly available wherever they are needed most.
        </p>
    </div>

</body>
</html>